package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func createTestClient(h http.HandlerFunc) (*client, *httptest.Server) {
	s := httptest.NewServer(h)
	c := &client{
		httpClient: s.Client(),
		baseUrl:    s.URL,

		retries: 1,
		tokenId: "testid",
		token:   "testtoken",
	}
	return c, s
}

func TestRequest(t *testing.T) {
	c, _ := createTestClient(func(rw http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/vnd.pterodactyl.v1+json", r.Header.Get("Accept"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer testid.testtoken", r.Header.Get("Authorization"))
		assert.Equal(t, "/test", r.URL.Path)

		rw.WriteHeader(http.StatusOK)
	})
	r, err := c.requestOnce(context.Background(), "", "/test", nil)
	assert.NoError(t, err)
	assert.NotNil(t, r)
}

func TestRequestRetry(t *testing.T) {
	// Test if the client retries failed requests
	i := 0
	c, _ := createTestClient(func(rw http.ResponseWriter, r *http.Request) {
		if i < 1 {
			rw.WriteHeader(http.StatusInternalServerError)
		} else {
			rw.WriteHeader(http.StatusOK)
		}
		i++
	})
	c.retries = 2
	r, err := c.request(context.Background(), "", "", nil)
	assert.NoError(t, err)
	assert.NotNil(t, r)
	assert.Equal(t, http.StatusOK, r.StatusCode)
	assert.Equal(t, 2, i)

	// Test whether the client returns the last request after retry limit is reached
	i = 0
	c, _ = createTestClient(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
		i++
	})
	c.retries = 2
	r, err = c.request(context.Background(), "get", "", nil)
	assert.NoError(t, err)
	assert.NotNil(t, r)
	assert.Equal(t, http.StatusInternalServerError, r.StatusCode)
	assert.Equal(t, 2, i)
}

func TestGet(t *testing.T) {
	c, _ := createTestClient(func(rw http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Len(t, r.URL.Query(), 1)
		assert.Equal(t, "world", r.URL.Query().Get("hello"))
	})
	r, err := c.get(context.Background(), "/test", q{"hello": "world"})
	assert.NoError(t, err)
	assert.NotNil(t, r)
}

func TestPost(t *testing.T) {
	test := map[string]string{
		"hello": "world",
	}
	c, _ := createTestClient(func(rw http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)

	})
	r, err := c.post(context.Background(), "/test", test)
	assert.NoError(t, err)
	assert.NotNil(t, r)
}
