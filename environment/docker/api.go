package docker

import (
	"context"
	"net/http"

	"emperror.dev/errors"
	"github.com/docker/docker/api/types"
	"github.com/goccy/go-json"
)

// ContainerInspect is a rough equivalent of Docker's client.ContainerInspect()
// but re-written to use a more performant JSON decoder. This is important since
// a large number of requests to this endpoint are spawned by Wings, and the
// standard "encoding/json" shows its performance woes badly even with single
// containers running.
func (e *Environment) ContainerInspect(ctx context.Context) (types.ContainerJSON, error) {
	var st types.ContainerJSON

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/containers/"+e.Id+"/json", nil)
	if err != nil {
		return st, errors.WithStack(err)
	}

	req.Host = "docker"
	req.URL.Host = "127.0.0.1"
	req.URL.Scheme = "http"

	res, err := e.client.HTTPClient().Do(req)
	if err != nil {
		return st, errors.WithStack(err)
	}

	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return st, errors.WithStack(err)
	}
	return st, nil
}