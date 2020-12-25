package system

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Runs a given work function every "d" duration until the provided context is canceled.
func Every(ctx context.Context, d time.Duration, work func(t time.Time)) {
	ticker := time.NewTicker(d)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case t := <-ticker.C:
				work(t)
			}
		}
	}()
}

func FormatBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(1024), 0
	for n := b / 1024; n >= 1024; n /= 1024 {
		div *= 1024
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

type AtomicBool struct {
	flag uint32
}

func (ab *AtomicBool) Set(v bool) {
	i := 0
	if v {
		i = 1
	}

	atomic.StoreUint32(&ab.flag, uint32(i))
}

func (ab *AtomicBool) Get() bool {
	return atomic.LoadUint32(&ab.flag) == 1
}

// AtomicString allows for reading/writing to a given struct field without having to worry
// about a potential race condition scenario. Under the hood it uses a simple sync.RWMutex
// to control access to the value.
type AtomicString struct {
	v  string
	mu sync.RWMutex
}

func NewAtomicString(v string) AtomicString {
	return AtomicString{v: v}
}

// Stores the string value passed atomically.
func (as *AtomicString) Store(v string) {
	as.mu.Lock()
	as.v = v
	as.mu.Unlock()
}

// Loads the string value and returns it.
func (as *AtomicString) Load() string {
	as.mu.RLock()
	defer as.mu.RUnlock()
	return as.v
}

func (as *AtomicString) UnmarshalText(b []byte) error {
	as.Store(string(b))
	return nil
}

//goland:noinspection GoVetCopyLock
func (as AtomicString) MarshalText() ([]byte, error) {
	return []byte(as.Load()), nil
}
