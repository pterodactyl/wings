package tokens

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// A token is accepted only once.
func TestTokenIsAcceptedOnce(t *testing.T) {
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if getTokenStore().IsValidToken("token-used-at-once") {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), accepted.Load())
}
