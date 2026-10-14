package filesystem

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A server being transferred keeps all of its files, including ones the
// denylist would refuse to write.
func TestExtractTransferKeepsDenylistedFiles(t *testing.T) {
	// NewFs sets up the configuration the filesystem reads.
	_, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })
	root := filepath.Join(rfs.root, "server")
	fs, err := New(root, 0, []string{"denied.txt"})
	require.NoError(t, err)
	fs.isTest = true

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"allowed.txt": "allowed", "denied.txt": "denied"} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	require.NoError(t, fs.ExtractTransfer(context.Background(), &buf))

	for _, name := range []string{"allowed.txt", "denied.txt"} {
		_, err := os.Stat(filepath.Join(root, name))
		require.NoError(t, err, name)
	}
}
