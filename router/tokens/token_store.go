package tokens

import (
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
)

type TokenStore struct {
	sync.Mutex
	cache *cache.Cache
}

var (
	_tokens     *TokenStore
	_tokensOnce sync.Once
)

// Returns the global unique token store cache. This is used to validate
// one time token usage by storing any received tokens in a local memory
// cache until they are ready to expire.
func getTokenStore() *TokenStore {
	_tokensOnce.Do(func() {
		_tokens = &TokenStore{
			cache: cache.New(time.Minute*60, time.Minute*5),
		}
	})

	return _tokens
}

// Checks if a token is valid or not.
func (t *TokenStore) IsValidToken(token string) bool {
	t.Lock()
	defer t.Unlock()

	_, exists := t.cache.Get(token)

	if !exists {
		t.cache.Set(token, "", time.Minute*60)
	}

	return !exists
}
