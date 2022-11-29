package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/pterodactyl/wings/internal/progress"
)

// PushArchiveToTarget POSTs the archive to the target node and returns the
// response body.
func (t *Transfer) PushArchiveToTarget(url, token string) ([]byte, error) {
	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()

	t.SendMessage("Preparing to stream server data to destination...")
	t.SetStatus(StatusProcessing)

	a, err := t.Archive()
	if err != nil {
		t.Error(err, "Failed to get archive for transfer.")
		return nil, errors.New("failed to get archive for transfer")
	}

	t.SendMessage("Streaming archive to destination...")

	// Send the upload progress to the websocket every 5 seconds.
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	go func(ctx context.Context, p *progress.Progress, tc *time.Ticker) {
		defer tc.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-tc.C:
				t.SendMessage("Uploading " + p.Progress(25))
			}
		}
	}(ctx2, a.Progress(), time.NewTicker(5*time.Second))

	// Create a new request using the pipe as the body.
	body, writer := io.Pipe()
	defer body.Close()
	defer writer.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)

	// Create a new multipart writer that writes the archive to the pipe.
	mp := multipart.NewWriter(writer)
	defer mp.Close()
	req.Header.Set("Content-Type", mp.FormDataContentType())

	// Create a new goroutine to write the archive to the pipe used by the
	// multipart writer.
	errChan := make(chan error)
	go func() {
		defer close(errChan)
		defer writer.Close()
		defer mp.Close()

		src, pw := io.Pipe()
		defer src.Close()
		defer pw.Close()

		h := sha256.New()
		tee := io.TeeReader(src, h)

		dest, err := mp.CreateFormFile("archive", "archive.tar.gz")
		if err != nil {
			errChan <- errors.New("failed to create form file")
			return
		}

		ch := make(chan error)
		go func() {
			defer close(ch)

			if _, err := io.Copy(dest, tee); err != nil {
				ch <- fmt.Errorf("failed to stream archive to destination: %w", err)
				return
			}

			t.Log().Debug("finished copying dest to tee")
		}()

		if err := a.Stream(ctx, pw); err != nil {
			errChan <- errors.New("failed to stream archive to pipe")
			return
		}
		t.Log().Debug("finished streaming archive to pipe")

		// Close the pipe writer early to release resources and ensure that the data gets flushed.
		_ = pw.Close()

		// Wait for the copy to finish before we continue.
		t.Log().Debug("waiting on copy to finish")
		if err := <-ch; err != nil {
			errChan <- err
			return
		}

		if err := mp.WriteField("checksum", hex.EncodeToString(h.Sum(nil))); err != nil {
			errChan <- errors.New("failed to stream checksum")
			return
		}

		cancel2()
		t.SendMessage("Finished streaming archive to destination.")

		if err := mp.Close(); err != nil {
			t.Log().WithError(err).Error("error while closing multipart writer")
		}
		t.Log().Debug("closed multipart writer")
	}()

	t.Log().Debug("sending archive to destination")
	client := http.Client{Timeout: 0}
	res, err := client.Do(req)
	if err != nil {
		t.Log().Debug("error while sending archive to destination")
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from destination: %d", res.StatusCode)
	}
	t.Log().Debug("waiting for stream to complete")
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err2 := <-errChan:
		t.Log().Debug("stream completed")
		if err != nil || err2 != nil {
			if err == context.Canceled {
				return nil, err
			}

			t.Log().WithError(err).Debug("failed to send archive to destination")
			return nil, fmt.Errorf("http error: %w, multipart error: %v", err, err2)
		}
		defer res.Body.Close()
		t.Log().Debug("received response from destination")

		v, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		if res.StatusCode != http.StatusOK {
			return nil, errors.New(string(v))
		}

		return v, nil
	}
}
