package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"github.com/pterodactyl/wings/server/filesystem"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/juju/ratelimit"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
)

type S3Backup struct {
	Backup
}

var _ BackupInterface = (*S3Backup)(nil)

func NewS3(client remote.Client, uuid string, ignore string) *S3Backup {
	return &S3Backup{
		Backup{
			client:  client,
			Uuid:    uuid,
			Ignore:  ignore,
			adapter: S3BackupAdapter,
		},
	}
}

// Remove removes a backup from the system.
func (s *S3Backup) Remove() error {
	return os.Remove(s.Path())
}

// WithLogContext attaches additional context to the log output for this backup.
func (s *S3Backup) WithLogContext(c map[string]interface{}) {
	s.logContext = c
}

// Generate creates a new backup on the disk, moves it into the S3 bucket via
// the provided presigned URL, and then deletes the backup from the disk.
func (s *S3Backup) Generate(basePath, ignore string) (*ArchiveDetails, error) {
	defer s.Remove()

	a := &filesystem.Archive{
		BasePath: basePath,
		Ignore:   ignore,
	}

	s.log().Info("creating backup for server...")
	if err := a.Create(s.Path()); err != nil {
		return nil, err
	}
	s.log().Info("created backup successfully")

	rc, err := os.Open(s.Path())
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	if err := s.generateRemoteRequest(rc); err != nil {
		return nil, err
	}

	return s.Details(), nil
}

// Reader provides a wrapper around an existing io.Reader
// but implements io.Closer in order to satisfy an io.ReadCloser.
type Reader struct {
	io.Reader
}

func (Reader) Close() error {
	return nil
}

// Generates the remote S3 request and begins the upload.
func (s *S3Backup) generateRemoteRequest(rc io.ReadCloser) error {
	defer rc.Close()

	s.log().Debug("attempting to get size of backup...")
	size, err := s.Backup.Size()
	if err != nil {
		return err
	}
	s.log().WithField("size", size).Debug("got size of backup")

	s.log().Debug("attempting to get S3 upload urls from Panel...")
	urls, err := s.client.GetBackupRemoteUploadURLs(context.Background(), s.Backup.Uuid, size)
	if err != nil {
		return err
	}
	s.log().Debug("got S3 upload urls from the Panel")
	s.log().WithField("parts", len(urls.Parts)).Info("attempting to upload backup to s3 endpoint...")

	handlePart := func(part string, size int64) (string, error) {
		r, err := http.NewRequest(http.MethodPut, part, nil)
		if err != nil {
			return "", err
		}

		r.ContentLength = size
		r.Header.Add("Content-Length", strconv.Itoa(int(size)))
		r.Header.Add("Content-Type", "application/x-gzip")

		// Limit the reader to the size of the part.
		r.Body = Reader{Reader: io.LimitReader(rc, size)}

		// This http request can block forever due to it not having a timeout,
		// but we are uploading up to 5GB of data, so there is not really
		// a good way to handle a timeout on this.
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()

		// Handle non-200 status codes.
		if res.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to put S3 object part, %d:%s", res.StatusCode, res.Status)
		}

		// Get the ETag from the uploaded part, this should be sent with the CompleteMultipartUpload request.
		return res.Header.Get("ETag"), nil
	}

	for i, part := range urls.Parts {
		// Get the size for the current part.
		var partSize int64
		if i+1 < len(urls.Parts) {
			partSize = urls.PartSize
		} else {
			// This is the remaining size for the last part,
			// there is not a minimum size limit for the last part.
			partSize = size - (int64(i) * urls.PartSize)
		}

		// Attempt to upload the part.
		if _, err := handlePart(part, partSize); err != nil {
			s.log().WithField("part_id", i+1).WithError(err).Warn("failed to upload part")
			return err
		}

		s.log().WithField("part_id", i+1).Info("successfully uploaded backup part")
	}

	s.log().WithField("parts", len(urls.Parts)).Info("backup has been successfully uploaded")

	return nil
}

// Restore will read from the provided reader assuming that it is a gzipped
// tar reader. When a file is encountered in the archive the callback function
// will be triggered. If the callback returns an error the entire process is
// stopped, otherwise this function will run until all files have been written.
//
// This restoration uses a workerpool to use up to the number of CPUs available
// on the machine when writing files to the disk.
func (s *S3Backup) Restore(r io.Reader, callback RestoreCallback) error {
	reader := r
	// Steal the logic we use for making backups which will be applied when restoring
	// this specific backup. This allows us to prevent overloading the disk unintentionally.
	if writeLimit := int64(config.Get().System.Backups.WriteLimit * 1024 * 1024); writeLimit > 0 {
		reader = ratelimit.Reader(r, ratelimit.NewBucketWithRate(float64(writeLimit), writeLimit))
	}
	gr, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if header.Typeflag == tar.TypeReg {
			if err := callback(header.Name, tr); err != nil {
				return err
			}
		}
	}
	return nil
}
