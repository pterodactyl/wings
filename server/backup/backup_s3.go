package backup

import (
	"bytes"
	"context"
	"emperror.dev/errors"
	"fmt"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/api"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
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
		return nil, errors.WithStackIf(err)
	}

	rc, err := os.Open(s.Path())
	if err != nil {
		return nil, errors.WithStackIf(err)
	}
	defer rc.Close()

	if err := s.generateRemoteRequest(rc); err != nil {
		return nil, errors.WithStackIf(err)
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

	log.WithFields(log.Fields{
		"backup_id": s.Uuid,
		"adapter":   "s3",
	}).Info("attempting to upload backup..")

	handlePart := func(part string, size int64) (string, error) {
		r, err := http.NewRequest(http.MethodPut, part, nil)
		if err != nil {
			return "", err
		}

		r.ContentLength = size
		r.Header.Add("Content-Length", strconv.Itoa(int(size)))
		r.Header.Add("Content-Type", "application/x-gzip")

		// Limit the reader to the size of the part.
		r.Body = Reader{io.LimitReader(rc, size)}

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

	// Start assembling the body that will be sent as apart of the CompleteMultipartUpload request.
	var completeUploadBody bytes.Buffer
	completeUploadBody.WriteString("<CompleteMultipartUpload>\n")

	partCount := len(urls.Parts)
	for i, part := range urls.Parts {
		// Get the size for the current part.
		var partSize int64
		if i+1 < partCount {
			partSize = urls.PartSize
		} else {
			// This is the remaining size for the last part,
			// there is not a minimum size limit for the last part.
			partSize = size - (int64(i) * urls.PartSize)
		}

		// Attempt to upload the part.
		etag, err := handlePart(part, partSize)
		if err != nil {
			log.WithError(err).Warn("failed to upload part")

			// Send an AbortMultipartUpload request.
			if err := s.finishUpload(urls.AbortMultipartUpload, nil); err != nil {
				log.WithError(err).Warn("failed to abort multipart backup upload")
			}

			return err
		}

		// Add the part to the CompleteMultipartUpload body.
		completeUploadBody.WriteString("\t<Part>\n")
		completeUploadBody.WriteString("\t\t<ETag>\"" + etag + "\"</ETag>\n")
		completeUploadBody.WriteString("\t\t<PartNumber>" + strconv.Itoa(i+1) + "</PartNumber>\n")
		completeUploadBody.WriteString("\t</Part>\n")
	}
	completeUploadBody.WriteString("</CompleteMultipartUpload>")

	// Send a CompleteMultipartUpload request.
	if err := s.finishUpload(urls.CompleteMultipartUpload, &completeUploadBody); err != nil {
		return err
	}

	log.WithFields(log.Fields{
		"backup_id": s.Uuid,
		"adapter":   "s3",
	}).Info("backup has been successfully uploaded")
	return nil
}

// finishUpload sends a requests to the specified url to either complete or abort the upload.
func (s *S3Backup) finishUpload(url string, body io.Reader) error {
	r, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return err
	}

	// Create a new http client with a 10 second timeout.
	c := &http.Client{
		Timeout: 10 * time.Second,
	}

	res, err := c.Do(r)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	// Handle non-200 status codes.
	if res.StatusCode != http.StatusOK {
		// If no body was sent, we were aborting the upload.
		if body == nil {
			return fmt.Errorf("failed to abort S3 multipart upload, %d:%s", res.StatusCode, res.Status)
		}

		// If a body was sent we were completing the upload.
		// TODO: Attempt to send abort request?
		return fmt.Errorf("failed to complete S3 multipart upload, %d:%s", res.StatusCode, res.Status)
	}

	return nil
}
