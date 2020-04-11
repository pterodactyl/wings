package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"go.uber.org/zap"
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

func (r *PanelRequest) Get(url string) (*http.Response, error) {
	c := r.GetClient()

	req, err := http.NewRequest(http.MethodGet, r.GetEndpoint(url), nil)
	req = r.SetHeaders(req)

	if err != nil {
		return nil, err
	}

	zap.S().Debugw("GET request to endpoint", zap.String("endpoint", r.GetEndpoint(url)), zap.Any("headers", req.Header))

	return c.Do(req)
}

func (r *PanelRequest) Post(url string, data []byte) (*http.Response, error) {
	c := r.GetClient()

	req, err := http.NewRequest(http.MethodPost, r.GetEndpoint(url), bytes.NewBuffer(data))
	req = r.SetHeaders(req)

	if err != nil {
		return nil, err
	}

	zap.S().Debugw("POST request to endpoint", zap.String("endpoint", r.GetEndpoint(url)), zap.Any("headers", req.Header))

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

type RequestError struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Returns the error response in a string form that can be more easily consumed.
func (re *RequestError) String() string {
	return fmt.Sprintf("%s: %s (HTTP/%s)", re.Code, re.Detail, re.Status)
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

	if len(bag.Errors) == 0 {
		return new(RequestError)
	}

	return &bag.Errors[0]
}
