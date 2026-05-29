package installer

import (
	"context"

	"emperror.dev/errors"
	"github.com/asaskevich/govalidator"

	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

type Installer struct {
	server            *server.Server
	StartOnCompletion bool
}

type ServerDetails struct {
	UUID              string `json:"uuid"`
	StartOnCompletion bool   `json:"start_on_completion"`
}

// New validates the received data to ensure that all the required fields
// have been passed along in the request. This should be manually run before
// calling Execute().
func New(ctx context.Context, manager *server.Manager, details ServerDetails) (*Installer, error) {
	if !govalidator.IsUUIDv4(details.UUID) {
		return nil, NewValidationError("uuid provided was not in a valid format")
	}

	c, err := manager.Client().GetServerConfiguration(ctx, details.UUID)
	if err != nil {
		if !remote.IsRequestError(err) {
			return nil, errors.WithStackIf(err)
		}
		return nil, errors.WrapIf(err, "installer: could not get server configuration from remote API")
	}

	// Create a new server instance using the configuration we wrote to the disk
	// so that everything gets instantiated correctly on the struct.
	s, err := manager.InitServer(c)
	if err != nil {
		return nil, errors.WrapIf(err, "installer: could not init server instance")
	}
	i := Installer{server: s, StartOnCompletion: details.StartOnCompletion}
	return &i, nil
}

// Server returns the server instance.
func (i *Installer) Server() *server.Server {
	return i.server
}
