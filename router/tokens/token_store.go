package tokens

import (
	"github.com/patrickmn/go-cache"
	"sync"
	"time"
)

type TokenStore struct {
	cache *cache.Cache
	mutex *sync.Mutex
}

var _tokens *TokenStore

// Returns the global unique token store cache. This is used to validate
// one time token usage by storing any received tokens in a local memory
// cache until they are ready to expire.
func getTokenStore() *TokenStore {
	if _tokens == nil {
		_tokens = &TokenStore{
			cache: cache.New(time.Minute*60, time.Minute*5),
			mutex: &sync.Mutex{},
		}
	}

	return _tokens
}

func (t *TokenStore) IsValidToken(token string) bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	_, exists := t.cache.Get(token)

	if !exists {
		t.cache.Add(token, "", time.Minute*60)
	}

	return !exists
}
