package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
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

	upgrader websocket.Upgrader

	// The authentication token defined in the config.yml file that allows
	// a request to perform any action aganist the daemon.
	token string
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

// Authenticates the request token aganist the given permission string, ensuring that
// if it is a server permission, the token has control over that server. If it is a global
// token, this will ensure that the request is using a properly signed global token.
func (rt *Router) AuthenticateToken(permission string, h httprouter.Handle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		t := strings.Split(permission, ":")[0]

		// Adds support for using this middleware on the websocket routes for servers. Those
		// routes don't support Authorization headers, per the spec, so we abuse the socket
		// protocol header and use that to pass the authorization token along to Wings without
		// exposing the token in the URL directly. Neat. 📸
		auth := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
		if r.Header.Get("Sec-WebSocket-Protocol") != "" {
			auth = []string{"Bearer", r.Header.Get("Sec-WebSocket-Protocol")}
		}

		if len(auth) != 2 || auth[0] != "Bearer" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "authorization failed", http.StatusUnauthorized)
			return
		}

		if t != "i" && t != "s" {
			zap.S().Warnw("could not match a permission string", zap.String("permission", permission), zap.String("route", r.URL.String()))

			// If for whatever reason we didn't match a permission string just
			// return a 404. This should only ever happen because of developer error.
			http.NotFound(w, r)

			return
		}

		// Try to match the request aganist the global token for the Daemon, regardless
		// of the permission type. If nothing is matched we will fall through to the Panel
		// API to try and validate permissions for a server.
		if t == "s" || t == "i" {
			if auth[1] == rt.token {
				h(w, r, ps)
				return
			}
		}

		// Happens because we don't have any of the server handling code here.
		http.Error(w, "not implemented", http.StatusNotImplemented)
		return
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

type CreateDirectoryRequest struct {
	Name string `json:"name"`
	Path string `json:"path"`
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
		if err != io.EOF && err != io.ErrUnexpectedEOF {
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
			if err := s.Environment.Start(); err != nil {
				zap.S().Error(err, zap.String("server", s.Uuid), zap.String("action", "start"))
			}
			break
		case "stop":
			if err := s.Environment.Stop(); err != nil {
				zap.S().Error(err, zap.String("server", s.Uuid), zap.String("action", "stop"))
			}
			break
		case "restart":
			break
		case "kill":
			if err := s.Environment.Terminate(os.Kill); err != nil {
				zap.S().Error(err, zap.String("server", s.Uuid), zap.String("action", "kill"))
			}
		}
	}(action.Action, s)

	w.WriteHeader(http.StatusAccepted)
}

// Return the last 1Kb of the server log file.
func (rt *Router) routeServerLogs(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.Servers.Get(ps.ByName("server"))

	l, _ := strconv.ParseInt(r.URL.Query().Get("size"), 10, 64)
	if l <= 0 {
		l = 2048
	}

	out, err := s.ReadLogfile(l)
	if err != nil {
		zap.S().Errorw("failed to read server log file", zap.Error(err))
		http.Error(w, "failed to read log", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(struct{Data []string `json:"data"`}{Data: out })
}

// Handle a request to get the contents of a file on the server.
func (rt *Router) routeServerFileRead(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.Servers.Get(ps.ByName("server"))

	cleaned, err := s.Filesystem.SafePath(ps.ByName("path"))
	if err != nil {
		http.Error(w, "could not determine path", http.StatusInternalServerError)
		return
	}

	st, err := os.Stat(cleaned)
	if err != nil {
		if !os.IsNotExist(err) {
			zap.S().Errorw("failed to stat file for reading", zap.String("path", ps.ByName("path")), zap.String("server", s.Uuid), zap.Error(err))
		}

		http.Error(w, "failed to stat file", http.StatusInternalServerError)
		return
	}

	f, err := os.OpenFile(cleaned, os.O_RDONLY, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			zap.S().Errorw("failed to open file for reading", zap.String("path", ps.ByName("path")), zap.String("server", s.Uuid), zap.Error(err))
		}

		http.Error(w, "failed to open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	// If a download parameter is included in the URL go ahead and attach the necessary headers
	// so that the file can be downloaded.
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", "attachment; filename=" + st.Name())
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(int(st.Size())))
	}

	bufio.NewReader(f).WriteTo(w)
}

// Lists the contents of a directory.
func (rt *Router) routeServerFileList(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.Servers.Get(ps.ByName("server"))

	stats, err := s.Filesystem.ListDirectory(ps.ByName("path"))
	if os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		zap.S().Errorw("failed to list contents of directory", zap.String("server", s.Uuid), zap.String("path", ps.ByName("path")), zap.Error(err))

		http.Error(w, "failed to list directory", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(stats)
}

// Creates a new directory for the server.
func (rt *Router) routeServerCreateDirectory(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.Servers.Get(ps.ByName("server"))
	defer r.Body.Close()

	dec := json.NewDecoder(r.Body)
	var data CreateDirectoryRequest

	if err := dec.Decode(&data); err != nil {
		// Don't flood the logs with error messages if someone sends through bad
		// JSON data. We don't really care.
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			zap.S().Errorw("failed to decode directory creation data", zap.Error(err))
		}

		http.Error(w, "could not parse data in request", http.StatusUnprocessableEntity)
		return
	}

	if err := s.Filesystem.CreateDirectory(data.Name, data.Path); err != nil {
		zap.S().Errorw("failed to create directory for server", zap.String("server", s.Uuid), zap.Error(err))

		http.Error(w, "an error was encountered while creating the directory", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Configures the router and all of the associated routes.
func (rt *Router) ConfigureRouter() *httprouter.Router {
	router := httprouter.New()

	router.GET("/", rt.routeIndex)
	router.GET("/api/servers", rt.AuthenticateToken("i:servers", rt.routeAllServers))
	router.GET("/api/servers/:server", rt.AuthenticateToken("s:view", rt.AuthenticateServer(rt.routeServer)))
	router.GET("/api/servers/:server/logs", rt.AuthenticateToken("s:logs", rt.AuthenticateServer(rt.routeServerLogs)))
	router.GET("/api/servers/:server/files/read/*path", rt.AuthenticateToken("s:files", rt.AuthenticateServer(rt.routeServerFileRead)))
	router.GET("/api/servers/:server/files/list/*path", rt.AuthenticateToken("s:files", rt.AuthenticateServer(rt.routeServerFileList)))
	router.POST("/api/servers/:server/files/create-directory", rt.AuthenticateToken("s:files", rt.AuthenticateServer(rt.routeServerCreateDirectory)))

	router.POST("/api/servers/:server/power", rt.AuthenticateToken("s:power", rt.AuthenticateServer(rt.routeServerPower)))

	router.GET("/api/servers/:server/ws", rt.AuthenticateToken("s:websocket", rt.AuthenticateServer(rt.routeWebsocket)))
	return router
}
