package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/system"
)

type Client interface {
	GetBackupRemoteUploadURLs(ctx context.Context, backup string, size int64) (BackupRemoteUploadResponse, error)
	GetInstallationScript(ctx context.Context, uuid string) (InstallationScript, error)
	GetServerConfiguration(ctx context.Context, uuid string) (ServerConfigurationResponse, error)
	GetServers(context context.Context, perPage int) ([]RawServerData, error)
	ResetServersState(ctx context.Context) error
	SetArchiveStatus(ctx context.Context, uuid string, successful bool) error
	SetBackupStatus(ctx context.Context, backup string, data BackupRequest) error
	SendRestorationStatus(ctx context.Context, backup string, successful bool) error
	SetInstallationStatus(ctx context.Context, uuid string, successful bool) error
	SetTransferStatus(ctx context.Context, uuid string, successful bool) error
	ValidateSftpCredentials(ctx context.Context, request SftpAuthRequest) (SftpAuthResponse, error)
}

type client struct {
	httpClient *http.Client
	baseUrl    string
	tokenId    string
	token      string
	attempts   int
}

// New returns a new HTTP request client that is used for making authenticated
// requests to the Panel that this instance is running under.
func New(base string, opts ...ClientOption) Client {
	c := client{
		baseUrl: strings.TrimSuffix(base, "/") + "/api/remote",
		httpClient: &http.Client{
			Timeout: time.Second * 15,
		},
		attempts: 1,
	}
	for _, opt := range opts {
		opt(&c)
	}
	return &c
}

// WithCredentials sets the credentials to use when making request to the remote
// API endpoint.
func WithCredentials(id, token string) ClientOption {
	return func(c *client) {
		c.tokenId = id
		c.token = token
	}
}

// WithHttpClient sets the underlying HTTP client instance to use when making
// requests to the Panel API.
func WithHttpClient(httpClient *http.Client) ClientOption {
	return func(c *client) {
		c.httpClient = httpClient
	}
}

// requestOnce creates a http request and executes it once. Prefer request()
// over this method when possible. It appends the path to the endpoint of the
// client and adds the authentication token to the request.
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

// request executes a http request and attempts when errors occur.
// It appends the path to the endpoint of the client and adds the authentication token to the request.
func (c *client) request(ctx context.Context, method, path string, body io.Reader, opts ...func(r *http.Request)) (res *Response, err error) {
	for i := 0; i < c.attempts; i++ {
		res, err = c.requestOnce(ctx, method, path, body, opts...)
		if err == nil &&
			res.StatusCode < http.StatusInternalServerError &&
			res.StatusCode != http.StatusTooManyRequests {
			break
		}
	}
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return
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

// Response is a custom response type that allows for commonly used error
// handling and response parsing from the Panel API. This just embeds the normal
// HTTP response from Go and we attach a few helper functions to it.
type Response struct {
	*http.Response
}

// HasError determines if the API call encountered an error. If no request has
// been made the response will be false. This function will evaluate to true if
// the response code is anything 300 or higher.
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
		return nil, errors.New("http: attempting to read missing response")
	}

	if r.Response.Body != nil {
		b, _ = ioutil.ReadAll(r.Response.Body)
	}

	r.Response.Body = ioutil.NopCloser(bytes.NewBuffer(b))

	return b, nil
}

// BindJSON binds a given interface with the data returned in the response. This
// is a shortcut for calling Read and then manually calling json.Unmarshal on
// the raw bytes.
func (r *Response) BindJSON(v interface{}) error {
	b, err := r.Read()
	if err != nil {
		return err
	}

	if err := json.Unmarshal(b, &v); err != nil {
		return errors.Wrap(err, "http: could not unmarshal response")
	}
	return nil
}

// Returns the first error message from the API call as a string. The error
// message will be formatted similar to the below example:
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

// Logs the request into the debug log with all of the important request bits.
// The authorization key will be cleaned up before being output.
func debugLogRequest(req *http.Request) {
	if l, ok := log.Log.(*log.Logger); ok && l.Level != log.DebugLevel {
		return
	}
	headers := make(map[string][]string)
	for k, v := range req.Header {
		if k != "Authorization" || len(v) == 0 || len(v[0]) == 0 {
			headers[k] = v
			continue
		}

		headers[k] = []string{"(redacted)"}
	}

	log.WithFields(log.Fields{
		"method":   req.Method,
		"endpoint": req.URL.String(),
		"headers":  headers,
	}).Debug("making request to external HTTP endpoint")
}
