package server

import (
	"runtime"

	"emperror.dev/errors"
	"github.com/gammazero/workerpool"

	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/pterodactyl/wings/parser"
)

// UpdateConfigurationFiles updates all the defined configuration files for
// a server automatically to ensure that they always use the specified values.
func (s *Server) UpdateConfigurationFiles() {
	pool := workerpool.New(runtime.NumCPU())

	s.Log().Debug("acquiring process configuration files...")
	files := s.ProcessConfiguration().ConfigurationFiles
	s.Log().Debug("acquired process configuration files")
	for _, cf := range files {
		f := cf

		pool.Submit(func() {
			if f.Parser == parser.File {
				if _, err := s.Filesystem().UnixFS().Stat(f.FileName); errors.Is(err, ufs.ErrNotExist) {
					s.Log().WithField("file_name", f.FileName).Debug("skipping text configuration file that does not exist yet")
					return
				}
			}

			file, err := s.Filesystem().UnixFS().Touch(f.FileName, ufs.O_RDWR|ufs.O_CREATE, 0o644)
			if err != nil {
				s.Log().WithField("file_name", f.FileName).WithField("error", err).Error("failed to open file for configuration")
				return
			}
			defer file.Close()

			if err := f.Parse(file); err != nil {
				s.Log().WithField("error", err).Error("failed to parse and update server configuration file")
			}

			s.Log().WithField("file_name", f.FileName).Debug("finished processing server configuration file")
		})
	}

	pool.StopWait()
}
