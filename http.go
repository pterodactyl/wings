package main

import (
	"encoding/json"
	"fmt"
	"github.com/julienschmidt/httprouter"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"io"
	"net/http"
	"os"
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

type PowerActionRequest struct {
	Action string `json:"action"`
}

func (pr *PowerActionRequest) IsValid() bool {
	return pr.Action == "start" || pr.Action == "stop" || pr.Action == "kill" || pr.Action == "restart"
}

// Handles a request to control the power state of a server. If the action being passed
// through is invalid a 404 is returned. Otherwise, a HTTP/202 Accepted response is returned
// and the actual power action is run asynchronously so that we don't have to block the
// request until a potentially slow operation completes.
//
// This is done because for the most part the Panel is using websockets to determine when
// things are happening, so theres no reason to sit and wait for a request to finish. We'll
// just see over the socket if something isn't working correctly.
func (rt *Router) routeServerPower(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.Servers.Get(ps.ByName("server"))
	defer r.Body.Close()

	dec := json.NewDecoder(r.Body)
	var action PowerActionRequest

	if err := dec.Decode(&action); err != nil {
		// Don't flood the logs with error messages if someone sends through bad
		// JSON data. We don't really care.
		if err != io.EOF {
			zap.S().Errorw("failed to decode power action", zap.Error(err))
		}

		http.Error(w, "could not parse power action from request", http.StatusInternalServerError)
		return
	}

	if !action.IsValid() {
		http.NotFound(w, r)
		return
	}

	// Pass the actual heavy processing off to a seperate thread to handle so that
	// we can immediately return a response from the server.
	go func(a string, s *server.Server) {
		switch a {
		case "start":
			if err := s.Environment().Start(); err != nil {
				zap.S().Error(err, zap.String("server", s.Uuid), zap.String("action", "start"))
			}
			break
		case "stop":
			if err := s.Environment().Stop(); err != nil {
				zap.S().Error(err, zap.String("server", s.Uuid), zap.String("action", "stop"))
			}
			break
		case "restart":
			break
		case "kill":
			if err := s.Environment().Terminate(os.Kill); err != nil {
				zap.S().Error(err, zap.String("server", s.Uuid), zap.String("action", "kill"))
			}
		}
	}(action.Action, s)

	w.WriteHeader(http.StatusAccepted)
}

func (rt *Router) ConfigureRouter() *httprouter.Router {
	router := httprouter.New()

	router.GET("/", rt.routeIndex)
	router.GET("/api/servers", rt.routeAllServers)
	router.GET("/api/servers/:server", rt.AuthenticateServer(rt.routeServer))
	router.POST("/api/servers/:server/power", rt.AuthenticateServer(rt.routeServerPower))

	return router
}
