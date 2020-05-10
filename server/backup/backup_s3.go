package backup

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"io"
	"net/http"
	"os"
	"strconv"
)

type S3Backup struct {
	Backup

	// The pre-signed upload endpoint for the generated backup. This must be
	// provided otherwise this request will fail. This allows us to keep all
	// of the keys off the daemon instances and the panel can handle generating
	// the credentials for us.
	PresignedUrl string
}

var _ BackupInterface = (*S3Backup)(nil)

// Generates a new backup on the disk, moves it into the S3 bucket via the provided
// presigned URL, and then deletes the backup from the disk.
func (s *S3Backup) Generate(included *IncludedFiles, prefix string) (*ArchiveDetails, error) {
	defer s.Remove()

	a := &Archive{
		TrimPrefix: prefix,
		Files:      included,
	}

	if err := a.Create(s.Path(), context.Background()); err != nil {
		return nil, err
	}

	rc, err := os.Open(s.Path())
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	if resp, err := s.generateRemoteRequest(rc); err != nil {
		return nil, err
	} else {
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to put S3 object, %d:%s", resp.StatusCode, resp.Status)
		}
	}

	return s.Details(), err
}

// Removes a backup from the system.
func (s *S3Backup) Remove() error {
	return os.Remove(s.Path())
}

// Generates the remote S3 request and begins the upload.
func (s *S3Backup) generateRemoteRequest(rc io.ReadCloser) (*http.Response, error) {
	r, err := http.NewRequest(http.MethodPut, s.PresignedUrl, nil)
	if err != nil {
		return nil, err
	}

	if sz, err := s.Size(); err != nil {
		return nil, err
	} else {
		r.ContentLength = sz
		r.Header.Add("Content-Length", strconv.Itoa(int(sz)))
		r.Header.Add("Content-Type", "application/x-gzip")
	}

	r.Body = rc

	zap.S().Debugw("uploading backup to remote S3 endpoint", zap.String("endpoint", s.PresignedUrl), zap.Any("headers", r.Header))

	return http.DefaultClient.Do(r)
}
