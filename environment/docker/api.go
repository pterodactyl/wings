package docker

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/versions"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/goccy/go-json"

	"github.com/pterodactyl/wings/config"
)

var (
	o           sync.Once
	cli         cliSettings
	fastEnabled bool
)

type cliSettings struct {
	enabled bool
	proto   string
	host    string
	scheme  string
	version string
}

func configure(c *client.Client) {
	o.Do(func() {
		fastEnabled = config.Get().Docker.UsePerformantInspect

		r := reflect.ValueOf(c).Elem()
		cli.proto = r.FieldByName("proto").String()
		cli.host = r.FieldByName("addr").String()
		cli.scheme = r.FieldByName("scheme").String()
		cli.version = r.FieldByName("version").String()
	})
}

// ContainerInspect is a rough equivalent of Docker's client.ContainerInspect()
// but re-written to use a more performant JSON decoder. This is important since
// a large number of requests to this endpoint are spawned by Wings, and the
// standard "encoding/json" shows its performance woes badly even with single
// containers running.
func (e *Environment) ContainerInspect(ctx context.Context) (types.ContainerJSON, error) {
	configure(e.client)

	// Support feature flagging of this functionality so that if something goes
	// wrong for now it is easy enough for people to switch back to the older method
	// of fetching stats.
	if !fastEnabled {
		return e.client.ContainerInspect(ctx, e.Id)
	}

	var st types.ContainerJSON
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/containers/"+e.Id+"/json", nil)
	if err != nil {
		return st, errors.WithStack(err)
	}

	if cli.proto == "unix" || cli.proto == "npipe" {
		req.Host = "docker"
	}

	req.URL.Host = cli.host
	req.URL.Scheme = cli.scheme

	res, err := e.client.HTTPClient().Do(req)
	if err != nil {
		if res == nil {
			return st, errdefs.Unknown(err)
		}
		return st, errdefs.FromStatusCode(err, res.StatusCode)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return st, errors.Wrap(err, "failed to read response body from Docker")
	}
	if err := parseErrorFromResponse(res, body); err != nil {
		return st, errdefs.FromStatusCode(err, res.StatusCode)
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return st, errors.WithStack(err)
	}
	return st, nil
}

// parseErrorFromResponse is a re-implementation of Docker's
// client.checkResponseErr() function.
func parseErrorFromResponse(res *http.Response, body []byte) error {
	if res.StatusCode >= 200 && res.StatusCode < 400 {
		return nil
	}

	var ct string
	if res.Header != nil {
		ct = res.Header.Get("Content-Type")
	}

	var emsg string
	if (cli.version == "" || versions.GreaterThan(cli.version, "1.23")) && ct == "application/json" {
		var errResp types.ErrorResponse
		if err := json.Unmarshal(body, &errResp); err != nil {
			return errors.WithStack(err)
		}
		emsg = strings.TrimSpace(errResp.Message)
	} else {
		emsg = strings.TrimSpace(string(body))
	}

	return errors.Wrap(errors.New(emsg), "Error response from daemon")
}
