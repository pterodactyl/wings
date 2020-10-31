package backup

import (
	"bytes"
	"context"
	"fmt"
	"github.com/apex/log"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"io"
	"net/http"
	"os"
	"strconv"
)

type S3Backup struct {
	Backup
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
		return nil, errors.WithStack(err)
	}

	rc, err := os.Open(s.Path())
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer rc.Close()

	if err := s.generateRemoteRequest(rc); err != nil {
		return nil, errors.WithStack(err)
	}

	return s.Details(), err
}

// Removes a backup from the system.
func (s *S3Backup) Remove() error {
	return os.Remove(s.Path())
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

	size, err := s.Backup.Size()
	if err != nil {
		return err
	}

	urls, err := api.New().GetBackupRemoteUploadURLs(s.Backup.Uuid, size)
	if err != nil {
		return err
	}

	log.Debug("attempting to upload backup to remote S3 endpoint")

	handlePart := func(part string, size int64) (string, error) {
		r, err := http.NewRequest(http.MethodPut, part, nil)
		if err != nil {
			return "", err
		}

		r.ContentLength = size
		r.Header.Add("Content-Length", strconv.Itoa(int(size)))
		r.Header.Add("Content-Type", "application/x-gzip")

		r.Body = Reader{io.LimitReader(rc, size)}

		res, err := http.DefaultClient.Do(r)
		if err != nil {
			return "", err
		}

		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to put S3 object part, %d:%s", res.StatusCode, res.Status)
		}

		return res.Header.Get("ETag"), nil
	}

	// Keep track of errors from individual part uploads.
	hasError := true
	defer func() {
		if !hasError {
			return
		}

		r, err := http.NewRequest(http.MethodPost, urls.AbortMultipartUpload, nil)
		if err != nil {
			log.WithError(err).Warn("failed to create http request (AbortMultipartUpload)")
			return
		}

		res, err := http.DefaultClient.Do(r)
		if err != nil {
			log.WithError(err).Warn("failed to make http request (AbortMultipartUpload)")
			return
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			log.Warnf("failed to abort S3 multipart upload, %d:%s", res.StatusCode, res.Status)
		}
	}()

	var completeBody bytes.Buffer
	completeBody.WriteString("<CompleteMultipartUpload>\n")

	partCount := len(urls.Parts)
	for i, part := range urls.Parts {
		var s int64
		if i+1 < partCount {
			s = urls.PartSize
		} else {
			s = size - (int64(i) * urls.PartSize)
		}

		etag, err := handlePart(part, s)
		if err != nil {
			return err
		}

		completeBody.WriteString("\t<Part>\n")
		completeBody.WriteString("\t\t<ETag>\"" + etag + "\"</ETag>\n")
		completeBody.WriteString("\t\t<PartNumber>" + strconv.Itoa(i+1) + "</PartNumber>\n")
		completeBody.WriteString("\t</Part>\n")
	}
	hasError = false

	completeBody.WriteString("</CompleteMultipartUpload>")

	r, err := http.NewRequest(http.MethodPost, urls.CompleteMultipartUpload, &completeBody)
	if err != nil {
		return err
	}

	res, err := http.DefaultClient.Do(r)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to complete S3 multipart upload, %d:%s", res.StatusCode, res.Status)
	}

	return nil
}
