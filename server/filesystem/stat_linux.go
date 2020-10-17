// +build !arm

package filesystem

import (
	"syscall"
	"time"
)

// Returns the time that the file/folder was created.
func (s *Stat) CTime() time.Time {
	st := s.Info.Sys().(*syscall.Stat_t)

	return time.Unix(st.Ctim.Sec, st.Ctim.Nsec)
}
