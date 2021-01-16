package filesystem

import (
	"time"
)

// On linux systems this will return the time that the file was created.
// However, I have no idea how to do this on windows, so we're skipping it
// for right now.
func (s *Stat) CTime() time.Time {
	return s.ModTime()
}
