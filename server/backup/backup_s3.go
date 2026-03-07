package backup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cenkalti/backoff/v4"
	"github.com/juju/ratelimit"
	"github.com/mholt/archives"

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
func (s *S3Backup) Generate(ctx context.Context, fsys *filesystem.Filesystem, ignore string) (*ArchiveDetails, error) {
	defer s.Remove()

	// Use our new ZIP-based BackupArchive from filesystem package (NOT the tar-based Archive)
	ba := &filesystem.BackupArchive{
		BaseDirectory: "/",
		Ignore:        ignore,
		Filesystem:    fsys,
	}

	s.log().WithField("path", s.Path()).Info("creating backup for server")

	// Create the backup file
	f, err := os.OpenFile(s.Path(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Apply write limiting if configured
	var writer io.Writer = f
	if writeLimit := int64(config.Get().System.Backups.WriteLimit * 1024 * 1024); writeLimit > 0 {
		writer = ratelimit.Writer(f, ratelimit.NewBucketWithRate(float64(writeLimit), writeLimit))
	}

	// Stream the ZIP backup to the file
	if err := ba.Stream(ctx, writer); err != nil {
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

// Restore will read from the provided reader assuming that it is a ZIP
// reader. When a file is encountered in the archive the callback function
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

	// Use ZIP format for restoration instead of tar.gz
	zipFormat := archives.Zip{}
	if err := zipFormat.Extract(ctx, reader, func(ctx context.Context, f archives.FileInfo) error {
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()

		return callback(f.NameInArchive, f.FileInfo, r)
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
func newS3FileUploader(rc io.ReadCloser) *s3FileUploader {
	return &s3FileUploader{
		ReadCloser:    rc,
		client:        &http.Client{Timeout: time.Hour * 6},
		uploadedParts: make([]remote.BackupPart, 0),
	}
}

func (fu *s3FileUploader) uploadPart(ctx context.Context, part string, size int64) (string, error) {
	var r io.Reader
	r = io.LimitReader(fu, size)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, part, r)
	if err != nil {
		return "", err
	}
	req.ContentLength = size

	// Exponential backoff loop for handling S3 uploads. This will attempt to upload
	// a given part up to 3 times with a backoff.
	bo := backoff.NewExponentialBackOff()
	bo.Multiplier = 1.5
	bo.MaxInterval = time.Second * 15
	bo.MaxElapsedTime = time.Minute * 2

	var res *http.Response
	err = backoff.Retry(func() error {
		r, err := fu.client.Do(req)
		if err != nil {
			return err
		}
		res = r
		// Don't retry if we were able to connect and got a response. The error handling
		// can occur below, but if we're communicating with S3 we shouldn't blindly retry.
		return nil
	}, backoff.WithContext(bo, ctx))
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	// Handle non-successful status codes and attempt to return a better error. Check
	// if the context has been canceled and don't return a malformed XML if that is
	// the case.
	if res.StatusCode != http.StatusOK {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("failed to upload part; got status %d; %s", res.StatusCode, func() string {
			b, _ := io.ReadAll(res.Body)
			return string(b)
		}())
	}

	if res.Header.Get("ETag") == "" {
		return "", errors.New("s3: received an empty etag on uploaded part")
	}

	// AWS returns the ETag in quotes, so we need to remove them.
	return strings.Trim(res.Header.Get("ETag"), `"`), nil
}
