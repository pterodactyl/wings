package filesystem

import (
	"time"
)

func (s *Stat) CTime() time.Time {
	st := s.Sys().(*syscall.Win32FileAttributeData)
	return time.Unix(int64(st.CreationTime.Nanoseconds()/1e9), int64(st.CreationTime.Nanoseconds()))
}
