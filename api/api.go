package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/system"
)

// Initializes the requester instance.
func New() *Request {
	return &Request{}
}

// A generic type allowing for easy binding use when making requests to API endpoints
// that only expect a singular argument or something that would not benefit from being
// a typed struct.
//
// Inspired by gin.H, same concept.
type D map[string]interface{}

// Same concept as D, but a map of strings, used for querying GET requests.
type Q map[string]string

// A custom API requester struct for Wings.
type Request struct{}

// A custom response type that allows for commonly used error handling and response
// parsing from the Panel API. This just embeds the normal HTTP response from Go and
// we attach a few helper functions to it.
type Response struct {
	*http.Response
}

// A pagination struct matching the expected pagination response from the Panel API.
type Pagination struct {
	CurrentPage uint `json:"current_page"`
	From        uint `json:"from"`
	LastPage    uint `json:"last_page"`
	PerPage     uint `json:"per_page"`
	To          uint `json:"to"`
	Total       uint `json:"total"`
}

// Builds the base request instance that can be used with the HTTP client.
func (r *Request) Client() *http.Client {
	return &http.Client{Timeout: time.Second * time.Duration(config.Get().RemoteQuery.Timeout)}
}

// Returns the given endpoint formatted as a URL to the Panel API.
func (r *Request) Endpoint(endpoint string) string {
	return fmt.Sprintf(
		"%s/api/remote/%s",
		strings.TrimSuffix(config.Get().PanelLocation, "/"),
		strings.TrimPrefix(strings.TrimPrefix(endpoint, "/"), "api/remote/"),
	)
}

// Makes a HTTP request to the given endpoint, attaching the necessary request headers from
// Wings to ensure that the request is properly handled by the Panel.
func (r *Request) Make(method, url string, body io.Reader, opts ...func(r *http.Request)) (*Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", fmt.Sprintf("Pterodactyl Wings/v%s (id:%s)", system.Version, config.Get().AuthenticationTokenId))
	req.Header.Set("Accept", "application/vnd.pterodactyl.v1+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s.%s", config.Get().AuthenticationTokenId, config.Get().AuthenticationToken))

	// Make any options calls that will allow us to make modifications to the request
	// before it is sent off.
	for _, cb := range opts {
		cb(req)
	}

	r.debug(req)

	res, err := r.Client().Do(req)

	return &Response{Response: res}, err
}

// Logs the request into the debug log with all of the important request bits.
// The authorization key will be cleaned up before being output.
func (r *Request) debug(req *http.Request) {
	headers := make(map[string][]string)
	for k, v := range req.Header {
		if k != "Authorization" || len(v) == 0 {
			headers[k] = v
			continue
		}

		headers[k] = []string{v[0][0:15] + "(redacted)"}
	}

	log.WithFields(log.Fields{
		"method":   req.Method,
		"endpoint": req.URL.String(),
		"headers":  headers,
	}).Debug("making request to external HTTP endpoint")
}

// Makes a GET request to the given Panel API endpoint. If any data is passed as the
// second argument it will be passed through on the request as URL parameters.
func (r *Request) Get(url string, data Q) (*Response, error) {
	return r.Make(http.MethodGet, r.Endpoint(url), nil, func(r *http.Request) {
		q := r.URL.Query()
		for k, v := range data {
			q.Set(k, v)
		}

		r.URL.RawQuery = q.Encode()
	})
}

// Makes a POST request to the given Panel API endpoint.
func (r *Request) Post(url string, data interface{}) (*Response, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	return r.Make(http.MethodPost, r.Endpoint(url), bytes.NewBuffer(b))
}

// Determines if the API call encountered an error. If no request has been made
// the response will be false. This function will evaluate to true if the response
// code is anything 300 or higher.
func (r *Response) HasError() bool {
	if r.Response == nil {
		return false
	}

	return r.StatusCode >= 300 || r.StatusCode < 200
}

// Reads the body from the response and returns it, then replaces it on the response
// so that it can be read again later. This does not close the response body, so any
// functions calling this should be sure to manually defer a Body.Close() call.
func (r *Response) Read() ([]byte, error) {
	var b []byte
	if r.Response == nil {
		return nil, errors.New("no response exists on interface")
	}

	if r.Response.Body != nil {
		b, _ = ioutil.ReadAll(r.Response.Body)
	}

	r.Response.Body = ioutil.NopCloser(bytes.NewBuffer(b))

	return b, nil
}

// Binds a given interface with the data returned in the response. This is a shortcut
// for calling Read and then manually calling json.Unmarshal on the raw bytes.
func (r *Response) Bind(v interface{}) error {
	b, err := r.Read()
	if err != nil {
		return err
	}

	return json.Unmarshal(b, &v)
}

// Returns the error message from the API call as a string. The error message will be formatted
// similar to the below example:
//
// HttpNotFoundException: The requested resource does not exist. (HTTP/404)
func (r *Response) Error() error {
	if !r.HasError() {
		return nil
	}

	var bag RequestErrorBag
	_ = r.Bind(&bag)

	e := &RequestError{}
	if len(bag.Errors) > 0 {
		e = &bag.Errors[0]
	}

	e.response = r.Response

	return e
}
