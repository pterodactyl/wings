package system

import "sync/atomic"

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
