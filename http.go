package main

import (
	"encoding/json"
	"fmt"
	"github.com/julienschmidt/httprouter"
	"github.com/pterodactyl/wings/server"
	"net/http"
)

type ServerCollection []*server.Server

// Retrieves a server out of the collection by UUID.
func (sc *ServerCollection) Get(uuid string) *server.Server {
	for _, s := range *sc {
		if s.Uuid == uuid {
			return s
		}
	}

	return nil
}

type Router struct {
	Servers ServerCollection
}

// Middleware to protect server specific routes. This will ensure that the server exists and
// is in a state that allows it to be exposed to the API.
func (rt *Router) AuthenticateServer(h httprouter.Handle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		if rt.Servers.Get(ps.ByName("server")) != nil {
			h(w, r, ps)
			return
		}

		http.NotFound(w, r)
	}
}

// Returns the basic Wings index page without anything else.
func (rt *Router) routeIndex(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	fmt.Fprint(w, "Welcome!\n")
}

// Returns all of the servers that exist on the Daemon. This route is only accessible to
// requests that include an administrative control key, otherwise a 404 is returned. This
// authentication is handled by a middleware.
func (rt *Router) routeAllServers(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	json.NewEncoder(w).Encode(rt.Servers)
}

// Returns basic information about a single server found on the Daemon.
func (rt *Router) routeServer(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	s := rt.Servers.Get(ps.ByName("server"))

	json.NewEncoder(w).Encode(s)
}

func (rt *Router) ConfigureRouter() *httprouter.Router {
	router := httprouter.New()

	router.GET("/", rt.routeIndex)
	router.GET("/api/servers", rt.routeAllServers)
	router.GET("/api/servers/:server", rt.AuthenticateServer(rt.routeServer))

	return router
}