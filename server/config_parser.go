package server

import (
	"github.com/pterodactyl/wings/parser"
	"go.uber.org/zap"
	"sync"
)

// Parent function that will update all of the defined configuration files for a server
// automatically to ensure that they always use the specified values.
func (s *Server) UpdateConfigurationFiles() {
	wg := new(sync.WaitGroup)

	for _, v := range s.processConfiguration.ConfigurationFiles {
		wg.Add(1)

		go func(f parser.ConfigurationFile, server *Server) {
			defer wg.Done()

			p, err := s.Filesystem.SafePath(f.FileName)
			if err != nil {
				zap.S().Errorw("failed to generate safe path for configuration file", zap.String("server", server.Uuid), zap.Error(err))

				return
			}

			if err := f.Parse(p, false); err != nil {
				zap.S().Errorw("failed to parse and update server configuration file", zap.String("server", server.Uuid), zap.Error(err))
			}
		}(v, s)
	}

	wg.Wait()
}