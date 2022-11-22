package backup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"emperror.dev/errors"
	"github.com/cenkalti/backoff/v4"
	"github.com/juju/ratelimit"
	"github.com/mholt/archiver/v4"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/filesystem"
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
func (s *S3Backup) Generate(ctx context.Context, basePath, ignore string) (*ArchiveDetails, error) {
	defer s.Remove()

	a := &filesystem.Archive{
		BasePath: basePath,
		Ignore:   ignore,
	}

	s.log().WithField("path", s.Path()).Info("creating backup for server")
	if err := a.Create(ctx, s.Path()); err != nil {
		return nil, err
	}
	s.log().Info("created backup successfully")

	rc, err := os.Open(s.Path())
	if err != nil {
		return nil, errors.Wrap(err, "backup: could not read archive from disk")
	}
	defer rc.Close()

	parts, err := s.generateRemoteRequest(ctx, rc)
	if err != nil {
		return nil, err
	}
	ad, err := s.Details(ctx, parts)
	if err != nil {
		return nil, errors.WrapIf(err, "backup: failed to get archive details after upload")
	}
	return ad, nil
}

// Restore will read from the provided reader assuming that it is a gzipped
// tar reader. When a file is encountered in the archive the callback function
// will be triggered. If the callback returns an error the entire process is
// stopped, otherwise this function will run until all files have been written.
//
// This restoration uses a workerpool to use up to the number of CPUs available
// on the machine when writing files to the disk.
func (s *S3Backup) Restore(ctx context.Context, r io.Reader, callback RestoreCallback) error {
	reader := r
	// Steal the logic we use for making backups which will be applied when restoring
	// this specific backup. This allows us to prevent overloading the disk unintentionally.
	if writeLimit := int64(config.Get().System.Backups.WriteLimit * 1024 * 1024); writeLimit > 0 {
		reader = ratelimit.Reader(r, ratelimit.NewBucketWithRate(float64(writeLimit), writeLimit))
	}
	if err := format.Extract(ctx, reader, nil, func(ctx context.Context, f archiver.File) error {
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()

		return callback(filesystem.ExtractNameFromArchive(f), f.FileInfo, r)
	}); err != nil {
		return err
	}
	return nil
}

// Generates the remote S3 request and begins the upload.
func (s *S3Backup) generateRemoteRequest(ctx context.Context, rc io.ReadCloser) ([]remote.BackupPart, error) {
	defer rc.Close()

	s.log().Debug("attempting to get size of backup...")
	size, err := s.Backup.Size()
	if err != nil {
		return nil, err
	}
	s.log().WithField("size", size).Debug("got size of backup")

	s.log().Debug("attempting to get S3 upload urls from Panel...")
	urls, err := s.client.GetBackupRemoteUploadURLs(context.Background(), s.Backup.Uuid, size)
	if err != nil {
		return nil, err
	}
	s.log().Debug("got S3 upload urls from the Panel")
	s.log().WithField("parts", len(urls.Parts)).Info("attempting to upload backup to s3 endpoint...")

	uploader := newS3FileUploader(rc)
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
		etag, err := uploader.uploadPart(ctx, part, partSize)
		if err != nil {
			s.log().WithField("part_id", i+1).WithError(err).Warn("failed to upload part")
			return nil, err
		}
		uploader.uploadedParts = append(uploader.uploadedParts, remote.BackupPart{
			ETag:       etag,
			PartNumber: i + 1,
		})
		s.log().WithField("part_id", i+1).Info("successfully uploaded backup part")
	}
	s.log().WithField("parts", len(urls.Parts)).Info("backup has been successfully uploaded")

	return uploader.uploadedParts, nil
}

type s3FileUploader struct {
	io.ReadCloser
	client        *http.Client
	uploadedParts []remote.BackupPart
}

// newS3FileUploader returns a new file uploader instance.
func newS3FileUploader(file io.ReadCloser) *s3FileUploader {
	return &s3FileUploader{
		ReadCloser: file,
		// We purposefully use a super high timeout on this request since we need to upload
		// a 5GB file. This assumes at worst a 10Mbps connection for uploading. While technically
		// you could go slower we're targeting mostly hosted servers that should have 100Mbps
		// connections anyways.
		client: &http.Client{Timeout: time.Hour * 2},
	}
}

// backoff returns a new expoential backoff implementation using a context that
// will also stop the backoff if it is canceled.
func (fu *s3FileUploader) backoff(ctx context.Context) backoff.BackOffContext {
	b := backoff.NewExponentialBackOff()
	b.Multiplier = 2
	b.MaxElapsedTime = time.Minute

	return backoff.WithContext(b, ctx)
}

// uploadPart attempts to upload a given S3 file part to the S3 system. If a
// 5xx error is returned from the endpoint this will continue with an exponential
// backoff to try and successfully upload the part.
//
// Once uploaded the ETag is returned to the caller.
func (fu *s3FileUploader) uploadPart(ctx context.Context, part string, size int64) (string, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPut, part, nil)
	if err != nil {
		return "", errors.Wrap(err, "backup: could not create request for S3")
	}

	r.ContentLength = size
	r.Header.Add("Content-Length", strconv.Itoa(int(size)))
	r.Header.Add("Content-Type", "application/x-gzip")

	// Limit the reader to the size of the part.
	r.Body = Reader{Reader: io.LimitReader(fu.ReadCloser, size)}

	var etag string
	err = backoff.Retry(func() error {
		res, err := fu.client.Do(r)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return backoff.Permanent(err)
			}
			// Don't use a permanent error here, if there is a temporary resolution error with
			// the URL due to DNS issues we want to keep re-trying.
			return errors.Wrap(err, "backup: S3 HTTP request failed")
		}
		_ = res.Body.Close()

		if res.StatusCode != http.StatusOK {
			err := errors.New(fmt.Sprintf("backup: failed to put S3 object: [HTTP/%d] %s", res.StatusCode, res.Status))
			// Only attempt a backoff retry if this error is because of a 5xx error from
			// the S3 endpoint. Any 4xx error should be treated as an error that a retry
			// would not fix.
			if res.StatusCode >= http.StatusInternalServerError {
				return err
			}
			return backoff.Permanent(err)
		}

		// Get the ETag from the uploaded part, this should be sent with the
		// CompleteMultipartUpload request.
		etag = res.Header.Get("ETag")

		return nil
	}, fu.backoff(ctx))

	if err != nil {
		if v, ok := err.(*backoff.PermanentError); ok {
			return "", v.Unwrap()
		}
		return "", err
	}
	return etag, nil
}

// Reader provides a wrapper around an existing io.Reader
// but implements io.Closer in order to satisfy an io.ReadCloser.
type Reader struct {
	io.Reader
}

func (Reader) Close() error {
	return nil
}
