package transfer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/server"
)

func newManagerTestTransfer(t *testing.T, uuid string) *Transfer {
	t.Helper()
	s, err := server.New(nil)
	require.NoError(t, err)
	s.Config().Uuid = uuid
	return New(context.Background(), s)
}

// Only one transfer can be tracked for a server, and removing one transfer does
// not remove a different transfer for the same server.
func TestManagerTracksOneTransferPerServer(t *testing.T) {
	m := NewManager()
	first := newManagerTestTransfer(t, "server")
	second := newManagerTestTransfer(t, "server")

	require.True(t, m.Add(first))
	require.False(t, m.Add(second))

	m.Remove(second)
	require.Same(t, first, m.Get("server"))

	m.Remove(first)
	require.Nil(t, m.Get("server"))
}
