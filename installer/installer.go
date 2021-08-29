package installer

import (
	"context"

	"emperror.dev/errors"
	"github.com/asaskevich/govalidator"
	"github.com/buger/jsonparser"

	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

type Installer struct {
	server *server.Server
}

// New validates the received data to ensure that all the required fields
// have been passed along in the request. This should be manually run before
// calling Execute().
func New(ctx context.Context, manager *server.Manager, data []byte) (*Installer, error) {
	uuid := getString(data, "uuid")
	if !govalidator.IsUUIDv4(uuid) {
		return nil, NewValidationError("uuid provided was not in a valid format")
	}

	c, err := manager.Client().GetServerConfiguration(ctx, uuid)
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
	return &Installer{server: s}, nil
}

// Uuid returns the UUID associated with this installer instance.
func (i *Installer) Uuid() string {
	return i.server.ID()
}

// Server returns the server instance.
func (i *Installer) Server() *server.Server {
	return i.server
}

// Returns a string value from the JSON data provided.
func getString(data []byte, key ...string) string {
	value, _ := jsonparser.GetString(data, key...)

	return value
}

// Returns an int value from the JSON data provided.
func getInt(data []byte, key ...string) int64 {
	value, _ := jsonparser.GetInt(data, key...)

	return value
}

func getBoolean(data []byte, key ...string) bool {
	value, _ := jsonparser.GetBoolean(data, key...)

	return value
}
