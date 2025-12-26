package system

import (
	"context"
	"sync"
)

type ctxHolder struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type ContextBag struct {
	mu    sync.Mutex
	ctx   context.Context
	items map[string]ctxHolder
}

func NewContextBag(ctx context.Context) *ContextBag {
	return &ContextBag{ctx: ctx, items: make(map[string]ctxHolder)}
}

// Context returns a context for the given key. If a value already exists in the
// internal map it is returned, otherwise a new cancelable context is returned.
// This context is shared between all callers until the cancel function is called
// by calling Cancel or CancelAll.
func (cb *ContextBag) Context(key string) context.Context {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if _, ok := cb.items[key]; !ok {
		ctx, cancel := context.WithCancel(cb.ctx)
		cb.items[key] = ctxHolder{ctx, cancel}
	}

	return cb.items[key].ctx
}

func (cb *ContextBag) Cancel(key string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if v, ok := cb.items[key]; ok {
		v.cancel()
		delete(cb.items, key)
	}
}

func (cb *ContextBag) CancelAll() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	for _, v := range cb.items {
		v.cancel()
	}

	cb.items = make(map[string]ctxHolder)
}
