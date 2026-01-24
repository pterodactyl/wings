package victorialogs

import (
	"sync"
	"time"
)

var (
	globalClient *Client
	clientMu     sync.RWMutex
)

func InitGlobal(enabled bool, url, username, password, environment string, batchSize int, flushInterval time.Duration) error {
	clientMu.Lock()
	defer clientMu.Unlock()

	if globalClient != nil {
		globalClient.Close()
	}

	if !enabled {
		globalClient = nil
		return nil
	}

	config := &Config{
		Enabled:       enabled,
		URL:           url,
		Username:      username,
		Password:      password,
		Environment:   environment,
		BatchSize:     batchSize,
		FlushInterval: flushInterval,
	}

	client, err := NewClient(config)
	if err != nil {
		return err
	}

	globalClient = client
	return nil
}

func GetGlobal() *Client {
	clientMu.RLock()
	defer clientMu.RUnlock()
	return globalClient
}

func CloseGlobal() {
	clientMu.Lock()
	defer clientMu.Unlock()

	if globalClient != nil {
		globalClient.Close()
		globalClient = nil
	}
}
