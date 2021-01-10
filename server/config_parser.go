package server

import (
	"runtime"

	"github.com/gammazero/workerpool"
)

// Parent function that will update all of the defined configuration files for a server
// automatically to ensure that they always use the specified values.
func (s *Server) UpdateConfigurationFiles() {
	pool := workerpool.New(runtime.NumCPU())

	files := s.ProcessConfiguration().ConfigurationFiles
	for _, cf := range files {
		f := cf

		pool.Submit(func() {
			p, err := s.Filesystem().SafePath(f.FileName)
			if err != nil {
				s.Log().WithField("error", err).Error("failed to generate safe path for configuration file")

				return
			}

			if err := f.Parse(p, false); err != nil {
				s.Log().WithField("error", err).Error("failed to parse and update server configuration file")
			}
		})
	}

	pool.StopWait()
}
