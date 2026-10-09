package filesystem

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/internal/ufs"
)

// openForWrite returns the size the opened file had, and only truncates it when
// asked to.
func TestOpenForWriteReturnsOpenedFileSize(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })

	const size = 4096
	require.NoError(t, rfs.CreateServerFile("a.bin", randomData(t, size)))

	f, got, err := fs.openForWrite("a.bin", ufs.O_RDWR, 0o644)
	require.NoError(t, err)
	require.Equal(t, int64(size), got)
	st, err := f.Stat()
	require.NoError(t, err)
	require.Equal(t, int64(size), st.Size())
	require.NoError(t, f.Close())

	f, got, err = fs.openForWrite("a.bin", ufs.O_RDWR|ufs.O_TRUNC, 0o644)
	require.NoError(t, err)
	require.Equal(t, int64(size), got)
	st, err = f.Stat()
	require.NoError(t, err)
	require.Zero(t, st.Size())
	require.NoError(t, f.Close())

	f, got, err = fs.openForWrite("new.bin", ufs.O_RDWR, 0o644)
	require.NoError(t, err)
	require.Zero(t, got)
	require.NoError(t, f.Close())
}
