package server

import (
	"github.com/pterodactyl/wings/parser"
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

			p, err := server.Filesystem.SafePath(f.FileName)
			if err != nil {
				server.Log().WithField("error", err).Error("failed to generate safe path for configuration file")

				return
			}

			if err := f.Parse(p, false); err != nil {
				server.Log().WithField("error", err).Error("failed to parse and update server configuration file")
			}
		}(v, s)
	}

	wg.Wait()
}