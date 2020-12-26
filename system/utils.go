package system

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
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
	v  bool
	mu sync.RWMutex
}

func NewAtomicBool(v bool) *AtomicBool {
	return &AtomicBool{v: v}
}

func (ab *AtomicBool) Store(v bool) {
	ab.mu.Lock()
	ab.v = v
	ab.mu.Unlock()
}

// Stores the value "v" if the current value stored in the AtomicBool is the opposite
// boolean value. If successfully swapped, the response is "true", otherwise "false"
// is returned.
func (ab *AtomicBool) SwapIf(v bool) bool {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	if ab.v != v {
		ab.v = v
		return true
	}
	return false
}

func (ab *AtomicBool) Load() bool {
	ab.mu.RLock()
	defer ab.mu.RUnlock()
	return ab.v
}

func (ab *AtomicBool) UnmarshalJSON(b []byte) error {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return json.Unmarshal(b, &ab.v)
}

func (ab *AtomicBool) MarshalJSON() ([]byte, error) {
	return json.Marshal(ab.Load())
}

// AtomicString allows for reading/writing to a given struct field without having to worry
// about a potential race condition scenario. Under the hood it uses a simple sync.RWMutex
// to control access to the value.
type AtomicString struct {
	v  string
	mu sync.RWMutex
}

func NewAtomicString(v string) *AtomicString {
	return &AtomicString{v: v}
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

func (as *AtomicString) UnmarshalJSON(b []byte) error {
	as.mu.Lock()
	defer as.mu.Unlock()
	return json.Unmarshal(b, &as.v)
}

func (as *AtomicString) MarshalJSON() ([]byte, error) {
	return json.Marshal(as.Load())
}
