package api

import (
	"fmt"
	"html"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/schrej/wings/config"
)

// API is a grouping struct for the api
type API struct {
}

// NewAPI creates a new Api object
func NewAPI() API {
	return API{}
}

// Listen starts the api http server
func (api *API) Listen() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, %q", html.EscapeString(r.URL.Path))
	})

	listenString := fmt.Sprintf("%s:%d", config.Get().Web.ListenHost, config.Get().Web.ListenPort)

	log.Info("Now listening on %s", listenString)
	log.Fatal(http.ListenAndServe(listenString, nil))
}
