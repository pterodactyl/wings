package main

import (
	"encoding/json"
	"fmt"
	"github.com/julienschmidt/httprouter"
	"github.com/pterodactyl/wings/server"
	"net/http"
)

type Router struct {
	Servers []*server.Server
}

func (r *Router) routeIndex(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	fmt.Fprint(w, "Welcome!\n")
}

func (r *Router) routeAllServers(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	enc := json.NewEncoder(w)
	enc.Encode(r.Servers)
}

func (r *Router) ConfigureRouter() *httprouter.Router {
	router := httprouter.New()

	router.GET("/", r.routeIndex)

	router.GET("/api/servers", r.routeAllServers)
	// router.GET("/api/servers/:server", r.routeServer)

	return router
}