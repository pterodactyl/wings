package backup

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
)

type S3Backup struct {
	// The UUID of this backup object. This must line up with a backup from
	// the panel instance.
	Uuid string

	// An array of files to ignore when generating this backup. This should be
	// compatible with a standard .gitignore structure.
	IgnoredFiles []string

	// The pre-signed upload endpoint for the generated backup. This must be
	// provided otherwise this request will fail. This allows us to keep all
	// of the keys off the daemon instances and the panel can handle generating
	// the credentials for us.
	PresignedUrl string
}

var _ Backup = (*S3Backup)(nil)

func (s *S3Backup) Identifier() string {
	return s.Uuid
}

func (s *S3Backup) Backup(included *IncludedFiles, prefix string) error {
	defer s.Remove()

	a := &Archive{
		TrimPrefix: prefix,
		Files:      included,
	}

	if err := a.Create(s.Path(), context.Background()); err != nil {
		return err
	}

	fmt.Println(s.PresignedUrl)

	r, err := http.NewRequest(http.MethodPut, s.PresignedUrl, nil)
	if err != nil {
		return err
	}

	if sz, err := s.Size(); err != nil {
		return err
	} else {
		r.ContentLength = sz
		r.Header.Add("Content-Length", strconv.Itoa(int(sz)))
		r.Header.Add("Content-Type", "application/x-gzip")
	}

	var rc io.ReadCloser
	if f, err := os.Open(s.Path()); err != nil {
		return err
	} else {
		rc = f
	}
	defer rc.Close()

	r.Body = rc
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(os.Stdout, resp.Body)
		return fmt.Errorf("failed to put S3 object, %d:%s", resp.StatusCode, resp.Status)
	}

	return nil
}

// Return the size of the generated backup.
func (s *S3Backup) Size() (int64, error) {
	st, err := os.Stat(s.Path())
	if err != nil {
		return 0, errors.WithStack(err)
	}

	return st.Size(), nil
}

// Returns the path for this specific backup. S3 backups are only stored on the disk
// long enough for us to get the details we need before uploading them to S3.
func (s *S3Backup) Path() string {
	return path.Join(config.Get().System.BackupDirectory, s.Uuid+".tmp")
}

// Returns the SHA256 checksum of a backup.
func (s *S3Backup) Checksum() ([]byte, error) {
	h := sha256.New()

	f, err := os.Open(s.Path())
	if err != nil {
		return []byte{}, errors.WithStack(err)
	}
	defer f.Close()

	if _, err := io.Copy(h, f); err != nil {
		return []byte{}, errors.WithStack(err)
	}

	return h.Sum(nil), nil
}

// Removes a backup from the system.
func (s *S3Backup) Remove() error {
	return os.Remove(s.Path())
}

func (s *S3Backup) Details() *ArchiveDetails {
	return &ArchiveDetails{
		Checksum: "checksum",
		Size:     1024,
	}
}

func (s *S3Backup) Ignored() []string {
	return s.IgnoredFiles
}
