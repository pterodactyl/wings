package server

import (
	"context"
	"sync"

	"github.com/apex/log"
)

type ctxHolder struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type SFTPConnectionBag struct {
	sync.Mutex
	ctx context.Context
	v   map[string]ctxHolder
}

// Sftp returns the SFTP connection bag for the server instance. This bag tracks
// all open SFTP connections by individual user and allows for a single user or
// all users to be disconnected by other processes.
func (s *Server) Sftp() *SFTPConnectionBag {
	s.Lock()
	defer s.Unlock()

	if s.sftpBag == nil {
		s.sftpBag = &SFTPConnectionBag{
			ctx: s.Context(),
			v:   make(map[string]ctxHolder),
		}
	}

	return s.sftpBag
}

// ContextFor returns a context for the specified user. This context is shared
// amongst all open SFTP connections that exist for the user. When the context is
// canceled all open SFTP connections should be closed.
func (d *SFTPConnectionBag) ContextFor(u string) context.Context {
	d.Lock()
	defer d.Unlock()

	if _, ok := d.v[u]; !ok {
		ctx, cancel := context.WithCancel(d.ctx)
		d.v[u] = ctxHolder{ctx, cancel}
	}

	log.WithField("u", u).Debug("sftp: context for")
	return d.v[u].ctx
}

// CancelFor cancels the context for the specified user.
func (d *SFTPConnectionBag) CancelFor(u string) {
	d.Lock()
	defer d.Unlock()

	if _, ok := d.v[u]; ok {
		d.v[u].cancel()
		delete(d.v, u)
	}
}

func (d *SFTPConnectionBag) CancelAll() {
	d.Lock()
	defer d.Unlock()

	for _, v := range d.v {
		v.cancel()
	}

	d.v = make(map[string]ctxHolder)
}
