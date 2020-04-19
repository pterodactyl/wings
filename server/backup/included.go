package backup

import (
	"os"
	"sync"
)

type IncludedFiles struct {
	sync.RWMutex
	files map[string]*os.FileInfo
}

// Pushes an additional file or folder onto the struct.
func (i *IncludedFiles) Push(info *os.FileInfo, p string) {
	i.Lock()
	defer i.Unlock()

	if i.files == nil {
		i.files = make(map[string]*os.FileInfo)
	}

	i.files[p] = info
}

// Returns all of the files that were marked as being included.
func (i *IncludedFiles) All() map[string]*os.FileInfo {
	i.RLock()
	defer i.RUnlock()

	return i.files
}
