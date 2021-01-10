package panelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	"github.com/pterodactyl/wings/system"
)

// A custom response type that allows for commonly used error handling and response
// parsing from the Panel API. This just embeds the normal HTTP response from Go and
// we attach a few helper functions to it.
type Response struct {
	*http.Response
}

// A generic type allowing for easy binding use when making requests to API endpoints
// that only expect a singular argument or something that would not benefit from being
// a typed struct.
//
// Inspired by gin.H, same concept.
type d map[string]interface{}

// Same concept as d, but a map of strings, used for querying GET requests.
type q map[string]string

// requestOnce creates a http request and executes it once.
// Prefer request() over this method when possible.
// It appends the path to the endpoint of the client and adds the authentication token to the request.
func (c *client) requestOnce(ctx context.Context, method, path string, body io.Reader, opts ...func(r *http.Request)) (*Response, error) {
	req, err := http.NewRequest(method, c.baseUrl+path, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", fmt.Sprintf("Pterodactyl Wings/v%s (id:%s)", system.Version, c.tokenId))
	req.Header.Set("Accept", "application/vnd.pterodactyl.v1+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s.%s", c.tokenId, c.token))

	// Call all opts functions to allow modifying the request
	for _, o := range opts {
		o(req)
	}

	debugLogRequest(req)

	res, err := c.httpClient.Do(req.WithContext(ctx))
	return &Response{res}, err
}

// request executes a http request and retries when errors occur.
// It appends the path to the endpoint of the client and adds the authentication token to the request.
func (c *client) request(ctx context.Context, method, path string, body io.Reader, opts ...func(r *http.Request)) (*Response, error) {
	var doErr error
	var res *Response

	for i := 0; i < c.retries; i++ {
		res, doErr = c.requestOnce(ctx, method, path, body, opts...)

		if doErr == nil &&
			res.StatusCode < http.StatusInternalServerError &&
			res.StatusCode != http.StatusTooManyRequests {
			break
		}
	}

	return res, doErr
}

// get executes a http get request.
func (c *client) get(ctx context.Context, path string, query q) (*Response, error) {
	return c.request(ctx, http.MethodGet, path, nil, func(r *http.Request) {
		q := r.URL.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		r.URL.RawQuery = q.Encode()
	})
}

// post executes a http post request.
func (c *client) post(ctx context.Context, path string, data interface{}) (*Response, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return c.request(ctx, http.MethodPost, path, bytes.NewBuffer(b))
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
func (r *Response) BindJSON(v interface{}) error {
	b, err := r.Read()
	if err != nil {
		return err
	}

	return json.Unmarshal(b, &v)
}

// Returns the first error message from the API call as a string.
// The error message will be formatted similar to the below example:
//
// HttpNotFoundException: The requested resource does not exist. (HTTP/404)
func (r *Response) Error() error {
	if !r.HasError() {
		return nil
	}

	var errs RequestErrors
	_ = r.BindJSON(&errs)

	e := &RequestError{}
	if len(errs.Errors) > 0 {
		e = &errs.Errors[0]
	}

	e.response = r.Response

	return e
}
