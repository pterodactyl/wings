package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/apex/log"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

// Initializes the requester instance.
func NewRequester() *PanelRequest {
	return &PanelRequest{
		Response: nil,
	}
}

type PanelRequest struct {
	Response *http.Response
}

// Builds the base request instance that can be used with the HTTP client.
func (r *PanelRequest) GetClient() *http.Client {
	return &http.Client{Timeout: time.Second * 30}
}

func (r *PanelRequest) SetHeaders(req *http.Request) *http.Request {
	req.Header.Set("Accept", "application/vnd.pterodactyl.v1+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s.%s", config.Get().AuthenticationTokenId, config.Get().AuthenticationToken))

	return req
}

func (r *PanelRequest) GetEndpoint(endpoint string) string {
	return fmt.Sprintf(
		"%s/api/remote/%s",
		strings.TrimSuffix(config.Get().PanelLocation, "/"),
		strings.TrimPrefix(strings.TrimPrefix(endpoint, "/"), "api/remote/"),
	)
}

// Logs the request into the debug log with all of the important request bits.
// The authorization key will be cleaned up before being output.
func (r *PanelRequest) logDebug(req *http.Request) {
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

func (r *PanelRequest) Get(url string) (*http.Response, error) {
	c := r.GetClient()

	req, err := http.NewRequest(http.MethodGet, r.GetEndpoint(url), nil)
	req = r.SetHeaders(req)

	if err != nil {
		return nil, err
	}

	r.logDebug(req)

	return c.Do(req)
}

func (r *PanelRequest) Post(url string, data []byte) (*http.Response, error) {
	c := r.GetClient()

	req, err := http.NewRequest(http.MethodPost, r.GetEndpoint(url), bytes.NewBuffer(data))
	req = r.SetHeaders(req)

	if err != nil {
		return nil, err
	}

	r.logDebug(req)

	return c.Do(req)
}

// Determines if the API call encountered an error. If no request has been made
// the response will be false.
func (r *PanelRequest) HasError() bool {
	if r.Response == nil {
		return false
	}

	return r.Response.StatusCode >= 300 || r.Response.StatusCode < 200
}

// Reads the body from the response and returns it, then replaces it on the response
// so that it can be read again later.
func (r *PanelRequest) ReadBody() ([]byte, error) {
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

func (r *PanelRequest) HttpResponseCode() int {
	if r.Response == nil {
		return 0
	}

	return r.Response.StatusCode
}

func IsRequestError(err error) bool {
	_, ok := err.(*RequestError)

	return ok
}

type RequestError struct {
	response *http.Response
	Code     string `json:"code"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
}

// Returns the error response in a string form that can be more easily consumed.
func (re *RequestError) Error() string {
	return fmt.Sprintf("Error response from Panel: %s: %s (HTTP/%d)", re.Code, re.Detail, re.response.StatusCode)
}

func (re *RequestError) String() string {
	return re.Error()
}

type RequestErrorBag struct {
	Errors []RequestError `json:"errors"`
}

// Returns the error message from the API call as a string. The error message will be formatted
// similar to the below example:
//
// HttpNotFoundException: The requested resource does not exist. (HTTP/404)
func (r *PanelRequest) Error() *RequestError {
	body, _ := r.ReadBody()

	bag := RequestErrorBag{}
	json.Unmarshal(body, &bag)

	e := new(RequestError)
	if len(bag.Errors) > 0 {
		e = &bag.Errors[0]
	}

	e.response = r.Response

	return e
}
