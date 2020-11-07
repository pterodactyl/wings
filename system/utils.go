package system

import (
	"sync"
	"sync/atomic"
)

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

func (as *AtomicString) UnmarshalText(b []byte) error {
	as.Store(string(b))
	return nil
}

func (as *AtomicString) MarshalText() ([]byte, error) {
	return []byte(as.Load()), nil
}
