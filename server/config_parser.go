package server

import (
	"go.uber.org/zap"
	"sync"
)

// Parent function that will update all of the defined configuration files for a server
// automatically to ensure that they always use the specified values.
func (s *Server) UpdateConfigurationFiles() {
	wg := new(sync.WaitGroup)

	for _, v := range s.processConfiguration.ConfigurationFiles {
		wg.Add(1)

		go func(server *Server) {
			defer wg.Done()

			p, err := s.Filesystem.SafePath(v.FileName)
			if err != nil {
				zap.S().Errorw("failed to generate safe path for configuration file", zap.String("server", server.Uuid), zap.Error(err))

				return
			}

			if err := v.Parse(p); err != nil {
				zap.S().Errorw("failed to parse and update server configuration file", zap.String("server", server.Uuid), zap.Error(err))
			}
		}(s)
	}

	wg.Wait()
}