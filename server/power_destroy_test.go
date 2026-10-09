package server

import (
	"testing"

	"emperror.dev/errors"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/system"
)

// Power actions that arrive while a server is being deleted return an error.
func TestPowerActionAfterCleanupForDestroyReturnsError(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	s.CleanupForDestroy()

	for _, action := range []PowerAction{PowerActionStart, PowerActionStop, PowerActionRestart, PowerActionTerminate} {
		var err error
		require.NotPanics(t, func() {
			err = s.HandlePowerAction(action)
		}, "action %s", action)
		require.True(t, errors.Is(err, system.ErrLockerDestroyed), "action %s returned %v", action, err)
	}

	// A second cleanup, from a repeated delete request, is a no-op.
	require.NotPanics(t, s.CleanupForDestroy)
	require.False(t, s.ExecutingPowerAction())
}
