package server

import (
	"os"
	"runtime"

	"github.com/gammazero/workerpool"
)

// UpdateConfigurationFiles updates all of the defined configuration files for
// a server automatically to ensure that they always use the specified values.
func (s *Server) UpdateConfigurationFiles() {
	pool := workerpool.New(runtime.NumCPU())
	files := s.ProcessConfiguration().ConfigurationFiles

	for _, cf := range files {
		f := cf

		pool.Submit(func() {
			fd, err := s.Filesystem().Touch(f.FileName, os.O_RDWR|os.O_CREATE, 0o644)
			if err != nil {
				s.Log().WithField("file_name", f.FileName).WithField("error", err).Error("failed to open configuration file")
				return
			}
			defer fd.Close()

			if err := f.Parse(fd); err != nil {
				s.Log().WithField("error", err).WithField("file_name", f.FileName).Error("failed to parse and update server configuration file")
			}

			s.Log().WithField("file_name", f.FileName).Debug("finished processing server configuration file")
		})
	}

	pool.StopWait()
}
