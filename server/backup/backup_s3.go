package backup

import (
	"fmt"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/server/filesystem"
	"io"
	"net/http"
	"os"
	"strconv"
)

type S3Backup struct {
	Backup
}

var _ BackupInterface = (*S3Backup)(nil)

// Removes a backup from the system.
func (s *S3Backup) Remove() error {
	return os.Remove(s.Path())
}

// Attaches additional context to the log output for this backup.
func (s *S3Backup) WithLogContext(c map[string]interface{}) {
	s.logContext = c
}

// Generates a new backup on the disk, moves it into the S3 bucket via the provided
// presigned URL, and then deletes the backup from the disk.
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
	urls, err := api.New().GetBackupRemoteUploadURLs(s.Backup.Uuid, size)
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
