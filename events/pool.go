package events

import (
	"reflect"

	"github.com/gammazero/workerpool"
)

type CallbackPool struct {
	callbacks []*func(Event)
	pool      *workerpool.WorkerPool
}

// Pushes a new callback into the array of listeners for the pool.
func (cp *CallbackPool) Add(callback *func(Event)) {
	if cp.index(reflect.ValueOf(callback)) < 0 {
		cp.callbacks = append(cp.callbacks, callback)
	}
}

// Removes a callback from the array of registered callbacks if it exists.
func (cp *CallbackPool) Remove(callback *func(Event)) {
	i := cp.index(reflect.ValueOf(callback))

	// If i < 0 it means there was no index found for the given callback, meaning it was
	// never registered or was already unregistered from the listeners. Also double check
	// that we didn't somehow escape the length of the topic callback (not sure how that
	// would happen, but lets avoid a panic condition).
	if i < 0 || i >= len(cp.callbacks) {
		return
	}

	// We can assume that the topic still exists at this point since we acquire an exclusive
	// lock on the process, and the "e.index" function cannot return a value >= 0 if there is
	// no topic already existing.
	cp.callbacks = append(cp.callbacks[:i], cp.callbacks[i+1:]...)
}

// Finds the index of a given callback in the topic by comparing all of the registered callback
// pointers to the passed function. This function does not aquire a lock as it should only be called
// within the confines of a function that has already acquired a lock for the duration of the lookup.
func (cp *CallbackPool) index(v reflect.Value) int {
	for i, handler := range cp.callbacks {
		if reflect.ValueOf(handler).Pointer() == v.Pointer() {
			return i
		}
	}

	return -1
}
