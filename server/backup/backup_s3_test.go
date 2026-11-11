package backup

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// A part whose first attempt fails with a 5xx error must be sent again with the
// same bytes, and the parts after it must not be shifted.
func TestS3UploadPartRetrySendsSameBytes(t *testing.T) {
	parts := [][]byte{
		bytes.Repeat([]byte("A"), 1024),
		bytes.Repeat([]byte("B"), 1024),
		bytes.Repeat([]byte("C"), 512),
	}
	const partSize = 1024

	f, err := os.Create(filepath.Join(t.TempDir(), "backup.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(bytes.Join(parts, nil)); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	attempts := map[string]int{}
	received := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		attempts[r.URL.Path]++
		if r.URL.Path == "/1" && attempts[r.URL.Path] == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		received[r.URL.Path] = body
		w.Header().Set("ETag", r.URL.Path)
	}))
	defer srv.Close()

	uploader := newS3FileUploader(f)
	for i, part := range parts {
		path := "/" + strconv.Itoa(i)
		etag, err := uploader.uploadPart(context.Background(), srv.URL+path, int64(i)*partSize, int64(len(part)))
		if err != nil {
			t.Fatalf("part %d: unexpected error: %v", i, err)
		}
		if etag != path {
			t.Errorf("part %d: expected ETag %q, got %q", i, path, etag)
		}

		mu.Lock()
		got := received[path]
		mu.Unlock()
		if !bytes.Equal(got, part) {
			t.Errorf("part %d: received %d bytes that do not match the part", i, len(got))
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if attempts["/1"] != 2 {
		t.Errorf("expected part 1 to be sent twice, got %d attempts", attempts["/1"])
	}
}
