package server

import (
	"syscall"
	"time"
)

// Returns the time that the file/folder was created.
func (s *Stat) CTime() time.Time {
	st := s.Info.Sys().(*syscall.Stat_t)

	return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec))
}