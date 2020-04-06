package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"github.com/buger/jsonparser"
	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
	"github.com/mholt/archiver/v3"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/installer"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Retrieves a server out of the collection by UUID.
func (rt *Router) GetServer(uuid string) *server.Server {
	return server.GetServers().Find(func(i *server.Server) bool {
		return i.Uuid == uuid
	})
}

type Router struct {
	upgrader websocket.Upgrader

	// The authentication token defined in the config.yml file that allows
	// a request to perform any action against the daemon.
	token string
}

func (rt *Router) AuthenticateRequest(h httprouter.Handle) httprouter.Handle {
	return rt.AuthenticateToken(rt.AuthenticateServer(h))
}

// Middleware to protect server specific routes. This will ensure that the server exists and
// is in a state that allows it to be exposed to the API.
func (rt *Router) AuthenticateServer(h httprouter.Handle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		if rt.GetServer(ps.ByName("server")) != nil {
			h(w, r, ps)
			return
		}

		http.NotFound(w, r)
	}
}

// Attaches required access control headers to all of the requests.
func (rt *Router) AttachAccessControlHeaders(w http.ResponseWriter, r *http.Request, ps httprouter.Params) (http.ResponseWriter, *http.Request, httprouter.Params) {
	w.Header().Set("Access-Control-Allow-Origin", config.Get().PanelLocation)
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

	return w, r, ps
}

// Authenticates the request token against the given permission string, ensuring that
// if it is a server permission, the token has control over that server. If it is a global
// token, this will ensure that the request is using a properly signed global token.
func (rt *Router) AuthenticateToken(h httprouter.Handle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		// Adds support for using this middleware on the websocket routes for servers. Those
		// routes don't support Authorization headers, per the spec, so we abuse the socket
		// protocol header and use that to pass the authorization token along to Wings without
		// exposing the token in the URL directly. Neat. 📸
		auth := strings.SplitN(r.Header.Get("Authorization"), " ", 2)

		if len(auth) != 2 || auth[0] != "Bearer" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "authorization failed", http.StatusUnauthorized)
			return
		}

		// Try to match the request against the global token for the Daemon, regardless
		// of the permission type. If nothing is matched we will fall through to the Panel
		// API to try and validate permissions for a server.
		if auth[1] == rt.token {
			h(rt.AttachAccessControlHeaders(w, r, ps))
			return
		}

		// Happens because we don't have any of the server handling code here.
		http.Error(w, "not implemented", http.StatusNotImplemented)
		return
	}
}

func (rt *Router) routeServerBackup(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))
	defer r.Body.Close()

	data := rt.ReaderToBytes(r.Body)
	b, err := s.NewBackup(data)
	if err != nil {
		zap.S().Errorw("failed to create backup struct for server", zap.String("server", s.Uuid), zap.Error(err))

		http.Error(w, "failed to update data structure", http.StatusInternalServerError)
		return
	}

	zap.S().Infow("starting backup process for server", zap.String("server", s.Uuid), zap.String("backup", b.Uuid))
	go func(bk *server.Backup) {
		if err := bk.BackupAndNotify(); err != nil {
			zap.S().Errorw("failed to generate backup for server", zap.Error(err))
		} else {
			zap.S().Infow("completed backup process for server", zap.String("backup", b.Uuid))
		}
	}(b)

	w.WriteHeader(http.StatusAccepted)
}

func (rt *Router) routeRequestServerArchive(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))

	go func() {
		start := time.Now()

		if err := s.Archiver.Archive(); err != nil {
			zap.S().Errorw("failed to get archive for server", zap.String("server", s.Uuid), zap.Error(err))
			return
		}

		zap.S().Debugw("successfully created archive for server", zap.String("server", s.Uuid), zap.Duration("time", time.Now().Sub(start).Round(time.Microsecond)))

		r := api.NewRequester()
		rerr, err := r.SendArchiveStatus(s.Uuid, true)
		if rerr != nil || err != nil {
			if err != nil {
				zap.S().Errorw("failed to notify panel with archive status", zap.String("server", s.Uuid), zap.Error(err))
				return
			}

			zap.S().Errorw("panel returned an error when sending the archive status", zap.String("server", s.Uuid), zap.Error(errors.New(rerr.String())))
			return
		}

		zap.S().Debugw("successfully notified panel about archive status", zap.String("server", s.Uuid))
	}()

	w.WriteHeader(http.StatusAccepted)
}

func (rt *Router) routeGetServerArchive(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	auth := strings.SplitN(r.Header.Get("Authorization"), " ", 2)

	if len(auth) != 2 || auth[0] != "Bearer" {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "authorization failed", http.StatusUnauthorized)
		return
	}

	token, err := ParseArchiveJWT([]byte(auth[1]))
	if err != nil {
		http.Error(w, "authorization failed", http.StatusUnauthorized)
		return
	}

	if token.Subject != ps.ByName("server") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	s := rt.GetServer(ps.ByName("server"))

	st, err := s.Archiver.Stat()
	if err != nil {
		if !os.IsNotExist(err) {
			zap.S().Errorw("failed to stat archive for reading", zap.String("server", s.Uuid), zap.Error(err))
			http.Error(w, "failed to stat archive", http.StatusInternalServerError)
			return
		}

		http.NotFound(w, r)
		return
	}

	checksum, err := s.Archiver.Checksum()
	if err != nil {
		zap.S().Errorw("failed to calculate checksum", zap.String("server", s.Uuid), zap.Error(err))
		http.Error(w, "failed to calculate checksum", http.StatusInternalServerError)
		return
	}

	file, err := os.Open(s.Archiver.ArchivePath())
	if err != nil {
		if !os.IsNotExist(err) {
			zap.S().Errorw("failed to open archive for reading", zap.String("server", s.Uuid), zap.Error(err))
		}

		http.Error(w, "failed to open archive", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("X-Checksum", checksum)
	w.Header().Set("X-Mime-Type", st.Mimetype)
	w.Header().Set("Content-Length", strconv.Itoa(int(st.Info.Size())))
	w.Header().Set("Content-Disposition", "attachment; filename="+s.Archiver.ArchiveName())
	w.Header().Set("Content-Type", "application/octet-stream")

	bufio.NewReader(file).WriteTo(w)
}

func (rt *Router) routeIncomingTransfer(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	zap.S().Debug("incoming transfer from panel!")
	defer r.Body.Close()

	go func(data []byte) {
		serverID, _ := jsonparser.GetString(data, "server_id")
		url, _ := jsonparser.GetString(data, "url")
		token, _ := jsonparser.GetString(data, "token")

		// Create an http client with no timeout.
		client := &http.Client{Timeout: 0}

		hasError := true
		defer func() {
			if !hasError {
				return
			}

			zap.S().Errorw("server transfer has failed", zap.String("server", serverID))
			rerr, err := api.NewRequester().SendTransferFailure(serverID)
			if rerr != nil || err != nil {
				if err != nil {
					zap.S().Errorw("failed to notify panel with transfer failure", zap.String("server", serverID), zap.Error(err))
					return
				}

				zap.S().Errorw("panel returned an error when notifying of a transfer failure", zap.String("server", serverID), zap.Error(errors.New(rerr.String())))
				return
			}

			zap.S().Debugw("successfully notified panel about transfer failure", zap.String("server", serverID))
		}()

		// Make a new GET request to the URL the panel gave us.
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			zap.S().Errorw("failed to create http request", zap.Error(err))
			return
		}

		// Add the authorization header.
		req.Header.Set("Authorization", token)

		// Execute the http request.
		res, err := client.Do(req)
		if err != nil {
			zap.S().Errorw("failed to send http request", zap.Error(err))
			return
		}
		defer res.Body.Close()

		// Handle non-200 status codes.
		if res.StatusCode != 200 {
			body, err := ioutil.ReadAll(res.Body)
			if err != nil {
				zap.S().Errorw("failed to read response body", zap.Int("status", res.StatusCode), zap.Error(err))
				return
			}

			zap.S().Errorw("failed to request server archive", zap.Int("status", res.StatusCode), zap.String("body", string(body)))
			return
		}

		// Get the path to the archive.
		archivePath := filepath.Join(config.Get().System.ArchiveDirectory, serverID + ".tar.gz")

		// Check if the archive already exists and delete it if it does.
		_, err = os.Stat(archivePath)
		if err != nil {
			if !os.IsNotExist(err) {
				zap.S().Errorw("failed to stat file", zap.Error(err))
				return
			}
		} else {
			if err := os.Remove(archivePath); err != nil {
				zap.S().Errorw("failed to delete old file", zap.Error(err))
				return
			}
		}

		// Create the file.
		file, err := os.Create(archivePath)
		if err != nil {
			zap.S().Errorw("failed to open file on disk", zap.Error(err))
			return
		}

		// Copy the file.
		_, err = io.Copy(file, res.Body)
		if err != nil {
			zap.S().Errorw("failed to copy file to disk", zap.Error(err))
			return
		}

		// Close the file so it can be opened to verify the checksum.
		if err := file.Close(); err != nil {
			zap.S().Errorw("failed to close archive file", zap.Error(err))
			return
		}
		zap.S().Debug("server archive has been downloaded, computing checksum..", zap.String("server", serverID))

		// Open the archive file for computing a checksum.
		file, err = os.Open(archivePath)
		if err != nil {
			zap.S().Errorw("failed to open file on disk", zap.Error(err))
			return
		}

		// Compute the sha256 checksum of the file.
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			zap.S().Errorw("failed to copy file for checksum verification", zap.Error(err))
			return
		}

		// Verify the two checksums.
		if hex.EncodeToString(hash.Sum(nil)) != res.Header.Get("X-Checksum") {
			zap.S().Errorw("checksum failed verification")
			return
		}

		// Close the file.
		if err := file.Close(); err != nil {
			zap.S().Errorw("failed to close archive file", zap.Error(err))
			return
		}

		zap.S().Infow("server archive transfer was successful", zap.String("server", serverID))

		// Get the server data from the request.
		serverData, t, _, _ := jsonparser.Get(data, "server")
		if t != jsonparser.Object {
			zap.S().Errorw("invalid server data passed in request")
			return
		}

		zap.S().Debug(string(serverData))

		// Create a new server installer (note this does not execute the install script)
		i, err := installer.New(serverData)
		if err != nil {
			zap.S().Warnw("failed to validate the received server data", zap.Error(err))
			return
		}

		// Add the server to the collection.
		server.GetServers().Add(i.Server())

		// Create the server's environment (note this does not execute the install script)
		i.Execute()

		// Un-archive the archive. That sounds weird..
		archiver.NewTarGz().Unarchive(archivePath, i.Server().Filesystem.Path())

		rerr, err := api.NewRequester().SendTransferSuccess(serverID)
		if rerr != nil || err != nil {
			if err != nil {
				zap.S().Errorw("failed to notify panel with transfer success", zap.String("server", serverID), zap.Error(err))
				return
			}

			zap.S().Errorw("panel returned an error when notifying of a transfer success", zap.String("server", serverID), zap.Error(errors.New(rerr.String())))
			return
		}

		zap.S().Debugw("successfully notified panel about transfer success", zap.String("server", serverID))
		hasError = false
	}(rt.ReaderToBytes(r.Body))

	w.WriteHeader(202)
}

func (rt *Router) ReaderToBytes(r io.Reader) []byte {
	buf := bytes.Buffer{}
	buf.ReadFrom(r)

	return buf.Bytes()
}

// Configures the router and all of the associated routes.
func (rt *Router) ConfigureRouter() *httprouter.Router {
	router := httprouter.New()

	router.GET("/download/backup", rt.routeDownloadBackup)
	router.POST("/api/servers/:server/backup", rt.AuthenticateRequest(rt.routeServerBackup))

	router.POST("/api/servers/:server/archive", rt.AuthenticateRequest(rt.routeRequestServerArchive))
	router.GET("/api/servers/:server/archive", rt.AuthenticateServer(rt.routeGetServerArchive))

	router.POST("/api/transfer", rt.AuthenticateToken(rt.routeIncomingTransfer))

	return router
}
