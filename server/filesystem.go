package server

import "path"

type Filesystem struct {
	// The root directory where all of the server data is contained. By default
	// this is going to be /srv/daemon-data but can vary depending on the system.
	Root string

	// The server object associated with this Filesystem.
	Server *Server
}

// Returns the root path that contains all of a server's data.
func (fs *Filesystem) Path() string {
	return path.Join(fs.Root, fs.Server.Uuid)
}

// Returns a safe path for a server object.
func (fs *Filesystem) SafePath(p string) string {
	return fs.Path()
}