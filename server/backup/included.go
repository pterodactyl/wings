package backup

import (
	"sync"
)

type IncludedFiles struct {
	sync.RWMutex
	files []string
}

// Pushes an additional file or folder onto the struct.
func (i *IncludedFiles) Push(p string) {
	i.Lock()
	i.files = append(i.files, p) // ~~
	i.Unlock()
}

// Returns all of the files that were marked as being included.
func (i *IncludedFiles) All() []string {
	i.RLock()
	defer i.RUnlock()

	return i.files
}
