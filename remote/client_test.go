package remote

import (
	"net/http"
	"net/http/httptest"
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
