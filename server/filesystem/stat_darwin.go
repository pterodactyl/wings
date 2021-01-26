package filesystem

import (
	"syscall"
	"time"
)

// CTime returns the time that the file/folder was created.
func (s *Stat) CTime() time.Time {
	st := s.Sys().(*syscall.Stat_t)

	return time.Unix(st.Ctimespec.Sec, st.Ctimespec.Nsec)
}
