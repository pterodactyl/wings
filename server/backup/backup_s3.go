package backup

import (
	"context"
	"fmt"
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

func (s *S3Backup) Generate(included *IncludedFiles, prefix string) error {
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
