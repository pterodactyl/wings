package main

import (
	"github.com/gbrlsnchs/jwt/v3"
	cache2 "github.com/patrickmn/go-cache"
	"sync"
	"time"
)

type JWTokens struct {
	cache *cache2.Cache
	mutex *sync.Mutex
}

var _tokens *JWTokens

type DownloadBackupPayload struct {
	jwt.Payload
	ServerUuid string `json:"server_uuid"`
	BackupUuid string `json:"backup_uuid"`
	UniqueId   string `json:"unique_id"`
}

func getTokenStore() *JWTokens {
	if _tokens == nil {
		_tokens = &JWTokens{
			cache: cache2.New(time.Minute*60, time.Minute*5),
			mutex: &sync.Mutex{},
		}
	}

	return _tokens
}

// Determines if a given JWT unique token is valid.
func (tokens *JWTokens) IsValidToken(token string) bool {
	tokens.mutex.Lock()
	defer tokens.mutex.Unlock()

	_, exists := tokens.cache.Get(token)

	if !exists {
		_tokens.cache.Add(token, "", time.Minute*60)
	}

	return !exists
}
