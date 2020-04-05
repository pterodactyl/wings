package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// Returns the basic Wings index page without anything else.
func (rt *Router) routeIndex(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	fmt.Fprint(w, "Welcome!\n")
}

// Returns all of the servers that exist on the Daemon. This route is only accessible to
// requests that include an administrative control key, otherwise a 404 is returned. This
// authentication is handled by a middleware.
func (rt *Router) routeAllServers(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	json.NewEncoder(w).Encode(server.GetServers().All())
}

// Returns basic information about a single server found on the Daemon.
func (rt *Router) routeServer(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))

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
	s := rt.GetServer(ps.ByName("server"))
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

	// Because we route all of the actual bootup process to a separate thread we need to
	// check the suspension status here, otherwise the user will hit the endpoint and then
	// just sit there wondering why it returns a success but nothing actually happens.
	//
	// We don't really care about any of the other actions at this point, they'll all result
	// in the process being stopped, which should have happened anyways if the server is suspended.
	if action.Action == "start" && s.Suspended {
		http.Error(w, "server is suspended", http.StatusBadRequest)
		return
	}

	// Pass the actual heavy processing off to a seperate thread to handle so that
	// we can immediately return a response from the server.
	go func(a string, s *server.Server) {
		switch a {
		case "start":
			if err := s.Environment.Start(); err != nil {
				zap.S().Errorw(
					"encountered unexpected error starting server process",
					zap.Error(err),
					zap.String("server", s.Uuid),
					zap.String("action", "start"),
				)
			}
			break
		case "stop":
			if err := s.Environment.Stop(); err != nil {
				zap.S().Errorw(
					"encountered unexpected error stopping server process",
					zap.Error(err),
					zap.String("server", s.Uuid),
					zap.String("action", "stop"),
				)
			}
			break
		case "restart":
			if err := s.Environment.WaitForStop(60, false); err != nil {
				zap.S().Errorw(
					"encountered unexpected error waiting for server process to stop",
					zap.Error(err),
					zap.String("server", s.Uuid),
					zap.String("action", "restart"),
				)
			}

			if err := s.Environment.Start(); err != nil {
				zap.S().Errorw(
					"encountered unexpected error starting server process",
					zap.Error(err),
					zap.String("server", s.Uuid),
					zap.String("action", "restart"),
				)
			}
			break
		case "kill":
			if err := s.Environment.Terminate(os.Kill); err != nil {
				zap.S().Errorw(
					"encountered unexpected error killing server process",
					zap.Error(err),
					zap.String("server", s.Uuid),
					zap.String("action", "kill"),
				)
			}
		}
	}(action.Action, s)

	w.WriteHeader(http.StatusAccepted)
}

// Return the last 1Kb of the server log file.
func (rt *Router) routeServerLogs(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))

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

	json.NewEncoder(w).Encode(struct{ Data []string `json:"data"` }{Data: out})
}

// Handle a request to get the contents of a file on the server.
func (rt *Router) routeServerFileRead(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))

	cleaned, err := s.Filesystem.SafePath(r.URL.Query().Get("file"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	st, err := s.Filesystem.Stat(cleaned)
	if err != nil {
		if !os.IsNotExist(err) {
			zap.S().Errorw("failed to stat file for reading", zap.String("path", ps.ByName("path")), zap.String("server", s.Uuid), zap.Error(err))

			http.Error(w, "failed to stat file", http.StatusInternalServerError)
			return
		}

		http.NotFound(w, r)
		return
	}

	f, err := os.Open(cleaned)
	if err != nil {
		if !os.IsNotExist(err) {
			zap.S().Errorw("failed to open file for reading", zap.String("path", ps.ByName("path")), zap.String("server", s.Uuid), zap.Error(err))
		}

		http.Error(w, "failed to open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("X-Mime-Type", st.Mimetype)
	w.Header().Set("Content-Length", strconv.Itoa(int(st.Info.Size())))

	// If a download parameter is included in the URL go ahead and attach the necessary headers
	// so that the file can be downloaded.
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", "attachment; filename="+st.Info.Name())
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	bufio.NewReader(f).WriteTo(w)
}

// Lists the contents of a directory.
func (rt *Router) routeServerListDirectory(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))

	stats, err := s.Filesystem.ListDirectory(r.URL.Query().Get("directory"))
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

// Writes a file to the system for the server.
func (rt *Router) routeServerWriteFile(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))

	p := r.URL.Query().Get("file")
	defer r.Body.Close()
	err := s.Filesystem.Writefile(p, r.Body)

	if err != nil {
		zap.S().Errorw("failed to write file to directory", zap.String("server", s.Uuid), zap.String("path", p), zap.Error(err))

		http.Error(w, "failed to write file to directory", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Creates a new directory for the server.
func (rt *Router) routeServerCreateDirectory(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))
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

func (rt *Router) routeServerRenameFile(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))
	defer r.Body.Close()

	data := rt.ReaderToBytes(r.Body)
	oldPath, _ := jsonparser.GetString(data, "rename_from")
	newPath, _ := jsonparser.GetString(data, "rename_to")

	if oldPath == "" || newPath == "" {
		http.Error(w, "invalid paths provided; did you forget to provide an old path and new path?", http.StatusUnprocessableEntity)
		return
	}

	if err := s.Filesystem.Rename(oldPath, newPath); err != nil {
		zap.S().Errorw("failed to rename file on server", zap.String("server", s.Uuid), zap.Error(err))

		http.Error(w, "an error occurred while renaming the file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (rt *Router) routeServerCopyFile(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))
	defer r.Body.Close()

	data := rt.ReaderToBytes(r.Body)
	loc, _ := jsonparser.GetString(data, "location")

	if err := s.Filesystem.Copy(loc); err != nil {
		zap.S().Errorw("error copying file for server", zap.String("server", s.Uuid), zap.Error(err))

		http.Error(w, "an error occurred while copying the file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (rt *Router) routeServerDeleteFile(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))
	defer r.Body.Close()

	data := rt.ReaderToBytes(r.Body)
	loc, _ := jsonparser.GetString(data, "location")

	if err := s.Filesystem.Delete(loc); err != nil {
		zap.S().Errorw("failed to delete a file or directory for server", zap.String("server", s.Uuid), zap.Error(err))

		http.Error(w, "an error occurred while trying to delete a file or directory", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (rt *Router) routeServerSendCommand(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))
	defer r.Body.Close()

	if running, err := s.Environment.IsRunning(); !running || err != nil {
		http.Error(w, "cannot send commands to a stopped instance", http.StatusBadGateway)
		return
	}

	data := rt.ReaderToBytes(r.Body)
	commands, dt, _, _ := jsonparser.Get(data, "commands")
	if dt != jsonparser.Array {
		http.Error(w, "commands must be an array of strings", http.StatusUnprocessableEntity)
		return
	}

	for _, command := range commands {
		if err := s.Environment.SendCommand(string(command)); err != nil {
			zap.S().Warnw("failed to send command to server", zap.Any("command", command), zap.Error(err))
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (rt *Router) routeServerInstall(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))
	defer r.Body.Close()

	go func(serv *server.Server) {
		if err := serv.Install(); err != nil {
			zap.S().Errorw("failed to execute server installation process", zap.String("server", s.Uuid), zap.Error(err))
		}
	}(s)

	w.WriteHeader(http.StatusAccepted)
}

func (rt *Router) routeServerUpdate(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))
	defer r.Body.Close()

	data := rt.ReaderToBytes(r.Body)
	if err := s.UpdateDataStructure(data, true); err != nil {
		zap.S().Errorw("failed to update a server's data structure", zap.String("server", s.Uuid), zap.Error(err))

		http.Error(w, "failed to update data structure", http.StatusInternalServerError)
		return
	}

	zap.S().Debugw("updated server's data structure", zap.String("server", s.Uuid))
	w.WriteHeader(http.StatusNoContent)
}

func (rt *Router) routeCreateServer(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	defer r.Body.Close()

	inst, err := installer.New(rt.ReaderToBytes(r.Body))

	if err != nil {
		zap.S().Warnw("failed to validate the received data", zap.Error(err))

		http.Error(w, "failed to validate data", http.StatusUnprocessableEntity)
		return
	}

	// Plop that server instance onto the request so that it can be referenced in
	// requests from here-on out.
	server.GetServers().Add(inst.Server())

	zap.S().Infow("beginning installation process for server", zap.String("server", inst.Uuid()))
	// Begin the installation process in the background to not block the request
	// cycle. If there are any errors they will be logged and communicated back
	// to the Panel where a reinstall may take place.
	go func(i *installer.Installer) {
		i.Execute()

		if err := i.Server().Install(); err != nil {
			zap.S().Errorw("failed to run install process for server", zap.String("server", i.Uuid()), zap.Error(err))
		}
	}(inst)

	w.WriteHeader(http.StatusAccepted)
}

func (rt *Router) routeServerReinstall(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))
	defer r.Body.Close()

	zap.S().Infow("beginning reinstall process for server", zap.String("server", s.Uuid))
	go func(server *server.Server) {
		if err := server.Reinstall(); err != nil {
			zap.S().Errorw("failed to complete server reinstall process", zap.String("server", server.Uuid), zap.Error(err))
		}
	}(s)

	w.WriteHeader(http.StatusAccepted)
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

func (rt *Router) routeSystemInformation(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	defer r.Body.Close()

	s, err := GetSystemInformation()
	if err != nil {
		zap.S().Errorw("failed to retrieve system information", zap.Error(errors.WithStack(err)))

		http.Error(w, "failed to retrieve information", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(s)
}

func (rt *Router) routeServerDelete(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s := rt.GetServer(ps.ByName("server"))
	defer r.Body.Close()

	// Immediately suspend the server to prevent a user from attempting
	// to start it while this process is running.
	s.Suspended = true

	// Delete the server's archive if it exists.
	if err := s.Archiver.DeleteIfExists(); err != nil {
		zap.S().Errorw("failed to delete server's archive", zap.String("server", s.Uuid), zap.Error(err))
		// We intentionally don't return here, if the archive fails to delete, the server can still be removed.
	}

	zap.S().Infow("processing server deletion request", zap.String("server", s.Uuid))
	// Destroy the environment; in Docker this will handle a running container and
	// forcibly terminate it before removing the container, so we do not need to handle
	// that here.
	if err := s.Environment.Destroy(); err != nil {
		zap.S().Errorw("failed to destroy server environment", zap.Error(errors.WithStack(err)))

		http.Error(w, "failed to destroy server environment", http.StatusInternalServerError)
		return
	}

	// Once the environment is terminated, remove the server files from the system. This is
	// done in a separate process since failure is not the end of the world and can be
	// manually cleaned up after the fact.
	//
	// In addition, servers with large amounts of files can take some time to finish deleting
	// so we don't want to block the HTTP call while waiting on this.
	go func(p string) {
		if err := os.RemoveAll(p); err != nil {
			zap.S().Warnw("failed to remove server files on deletion", zap.String("path", p), zap.Error(errors.WithStack(err)))
		}
	}(s.Filesystem.Path())

	var uuid = s.Uuid
	server.GetServers().Remove(func(s2 *server.Server) bool {
		return s2.Uuid == uuid
	})

	s = nil

	// Remove the configuration file stored on the Daemon for this server.
	go func(u string) {
		if err := os.Remove("data/servers/" + u + ".yml"); err != nil {
			zap.S().Warnw("failed to delete server configuration file on deletion", zap.String("server", u), zap.Error(errors.WithStack(err)))
		}
	}(uuid)

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

	router.OPTIONS("/api/system", func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		rt.AttachAccessControlHeaders(w, r, ps)
	})

	router.GET("/", rt.routeIndex)
	router.GET("/api/system", rt.AuthenticateToken(rt.routeSystemInformation))
	router.GET("/api/servers", rt.AuthenticateToken(rt.routeAllServers))
	router.GET("/api/servers/:server", rt.AuthenticateRequest(rt.routeServer))
	router.GET("/api/servers/:server/ws", rt.AuthenticateServer(rt.routeWebsocket))
	router.GET("/api/servers/:server/logs", rt.AuthenticateRequest(rt.routeServerLogs))
	router.GET("/api/servers/:server/files/contents", rt.AuthenticateRequest(rt.routeServerFileRead))
	router.GET("/api/servers/:server/files/list-directory", rt.AuthenticateRequest(rt.routeServerListDirectory))
	router.PUT("/api/servers/:server/files/rename", rt.AuthenticateRequest(rt.routeServerRenameFile))
	router.POST("/api/servers", rt.AuthenticateToken(rt.routeCreateServer))
	router.POST("/api/servers/:server/install", rt.AuthenticateRequest(rt.routeServerInstall))
	router.POST("/api/servers/:server/files/copy", rt.AuthenticateRequest(rt.routeServerCopyFile))
	router.POST("/api/servers/:server/files/write", rt.AuthenticateRequest(rt.routeServerWriteFile))
	router.POST("/api/servers/:server/files/create-directory", rt.AuthenticateRequest(rt.routeServerCreateDirectory))
	router.POST("/api/servers/:server/files/delete", rt.AuthenticateRequest(rt.routeServerDeleteFile))
	router.POST("/api/servers/:server/power", rt.AuthenticateRequest(rt.routeServerPower))
	router.POST("/api/servers/:server/commands", rt.AuthenticateRequest(rt.routeServerSendCommand))
	router.POST("/api/servers/:server/reinstall", rt.AuthenticateRequest(rt.routeServerReinstall))
	router.POST("/api/servers/:server/backup", rt.AuthenticateRequest(rt.routeServerBackup))
	router.PATCH("/api/servers/:server", rt.AuthenticateRequest(rt.routeServerUpdate))
	router.DELETE("/api/servers/:server", rt.AuthenticateRequest(rt.routeServerDelete))

	router.POST("/api/servers/:server/archive", rt.AuthenticateRequest(rt.routeRequestServerArchive))
	router.GET("/api/servers/:server/archive", rt.AuthenticateServer(rt.routeGetServerArchive))

	router.POST("/api/transfer", rt.AuthenticateToken(rt.routeIncomingTransfer))

	return router
}
