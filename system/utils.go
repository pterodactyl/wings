package system

import (
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
	v atomic.Value
}

// Returns a new instance of an AtomicString.
func NewAtomicString(v string) *AtomicString {
	as := &AtomicString{}
	if v != "" {
		as.Store(v)
	}
	return as
}

// Stores the string value passed atomically.
func (as *AtomicString) Store(v string) {
	as.v.Store(v)
}

// Loads the string value and returns it.
func (as *AtomicString) Load() string {
	if v := as.v.Load(); v != nil {
		return v.(string)
	}
	return ""
}
