package router

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"emperror.dev/errors"
	"encoding/hex"
	"github.com/apex/log"
	"github.com/buger/jsonparser"
	"github.com/gin-gonic/gin"
	"github.com/mholt/archiver/v3"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/installer"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func getServerArchive(c *gin.Context) {
	auth := strings.SplitN(c.GetHeader("Authorization"), " ", 2)

	if len(auth) != 2 || auth[0] != "Bearer" {
		c.Header("WWW-Authenticate", "Bearer")
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "The required authorization heads were not present in the request.",
		})
		return
	}

	token := tokens.TransferPayload{}
	if err := tokens.ParseToken([]byte(auth[1]), &token); err != nil {
		NewTrackedError(err).Abort(c)
		return
	}

	if token.Subject != c.Param("server") {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "( .. •˘___˘• .. )",
		})
		return
	}

	s := GetServer(c.Param("server"))

	st, err := s.Archiver.Stat()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			NewServerError(err, s).SetMessage("failed to stat archive").Abort(c)
			return
		}

		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	checksum, err := s.Archiver.Checksum()
	if err != nil {
		NewServerError(err, s).SetMessage("failed to calculate checksum").Abort(c)
		return
	}

	file, err := os.Open(s.Archiver.Path())
	if err != nil {
		tserr := NewServerError(err, s)
		if !os.IsNotExist(err) {
			tserr.SetMessage("failed to open archive for reading")
		} else {
			tserr.SetMessage("failed to open archive")
		}

		tserr.Abort(c)
		return
	}
	defer file.Close()

	c.Header("X-Checksum", checksum)
	c.Header("X-Mime-Type", st.Mimetype)
	c.Header("Content-Length", strconv.Itoa(int(st.Info.Size())))
	c.Header("Content-Disposition", "attachment; filename="+s.Archiver.Name())
	c.Header("Content-Type", "application/octet-stream")

	bufio.NewReader(file).WriteTo(c.Writer)
}

func postServerArchive(c *gin.Context) {
	s := GetServer(c.Param("server"))

	go func(s *server.Server) {
		r := api.New()
		l := log.WithField("server", s.Id())

		s.Events().Publish(server.SendTransferLogsEvent, "Creating archive of server..")

		// Attempt to get an archive of the server.  This **WILL NOT** modify the source files of a server,
		// this process is 100% safe and will not corrupt a server's files if it fails.
		if err := s.Archiver.Archive(); err != nil {
			l.WithField("error", err).Error("failed to get transfer archive for server")

			if err := r.SendArchiveStatus(s.Id(), false); err != nil {
				if !api.IsRequestError(err) {
					l.WithField("error", err).Error("failed to notify panel of failed archive status")
					return
				}

				l.WithField("error", err.Error()).Error("panel returned an error when notifying it of a failed archive status")
				return
			}

			l.Info("successfully notified panel of failed archive status")
			return
		}

		l.Info("successfully created server transfer archive, notifying panel..")

		if err := r.SendArchiveStatus(s.Id(), true); err != nil {
			if !api.IsRequestError(err) {
				l.WithField("error", err).Error("failed to notify panel of successful archive status")
				return
			}

			l.WithField("error", err.Error()).Error("panel returned an error when notifying it of a successful archive status")
			return
		}

		l.Info("successfully notified panel of successful transfer archive status")
	}(s)

	c.Status(http.StatusAccepted)
}

func postTransfer(c *gin.Context) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(c.Request.Body); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	go func(data []byte) {
		serverID, _ := jsonparser.GetString(data, "server_id")
		url, _ := jsonparser.GetString(data, "url")
		token, _ := jsonparser.GetString(data, "token")

		l := log.WithField("server", serverID)
		l.Info("incoming transfer for server")
		// Create an http client with no timeout.
		client := &http.Client{Timeout: 0}

		hasError := true
		defer func() {
			if !hasError {
				return
			}

			l.Info("server transfer failed, notifying panel")
			if err := api.New().SendTransferFailure(serverID); err != nil {
				if !api.IsRequestError(err) {
					l.WithField("error", err).Error("failed to notify panel with transfer failure")
					return
				}

				l.WithField("error", err.Error()).Error("received error response from panel while notifying of transfer failure")
				return
			}

			l.Debug("notified panel of transfer failure")
		}()

		// Make a new GET request to the URL the panel gave us.
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.WithField("error", err).Error("failed to create http request for archive transfer")
			return
		}

		// Add the authorization header.
		req.Header.Set("Authorization", token)

		l.Info("requesting archive for server transfer..")
		// Execute the http request.
		res, err := client.Do(req)
		if err != nil {
			l.WithField("error", err).Error("failed to send archive http request")
			return
		}
		defer res.Body.Close()

		// Handle non-200 status codes.
		if res.StatusCode != 200 {
			_, err := ioutil.ReadAll(res.Body)
			if err != nil {
				l.WithField("error", err).WithField("status", res.StatusCode).Error("failed read transfer response body")
				return
			}

			l.WithField("error", err).WithField("status", res.StatusCode).Error("failed to request server archive")
			return
		}

		// Get the path to the archive.
		archivePath := filepath.Join(config.Get().System.ArchiveDirectory, serverID+".tar.gz")

		// Check if the archive already exists and delete it if it does.
		if _, err = os.Stat(archivePath); err != nil {
			if !os.IsNotExist(err) {
				l.WithField("error", err).Error("failed to stat archive file")
				return
			}
		} else if err := os.Remove(archivePath); err != nil {
			l.WithField("error", err).Warn("failed to remove old archive file")
			return
		}

		// Create the file.
		file, err := os.Create(archivePath)
		if err != nil {
			l.WithField("error", err).Error("failed to open archive on disk")
			return
		}

		l.Info("writing transfer archive to disk..")
		// Copy the file.
		buf := make([]byte, 1024*4)
		_, err = io.CopyBuffer(file, res.Body, buf)
		if err != nil {
			l.WithField("error", err).Error("failed to copy archive file to disk")
			return
		}

		// Close the file so it can be opened to verify the checksum.
		if err := file.Close(); err != nil {
			l.WithField("error", err).Error("failed to close archive file")
			return
		}
		l.Info("finished writing transfer archive to disk")

		// Whenever the transfer fails or succeeds, delete the temporary transfer archive.
		defer func() {
			log.WithField("server", serverID).Debug("deleting temporary transfer archive..")
			if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
				l.WithFields(log.Fields{
					"server": serverID,
					"error":  err,
				}).Warn("failed to delete transfer archive")
			} else {
				l.Debug("deleted temporary transfer archive successfully")
			}
		}()

		l.Info("server transfer archive downloaded, computing checksum...")

		// Open the archive file for computing a checksum.
		file, err = os.Open(archivePath)
		if err != nil {
			l.WithField("error", err).Error("failed to open archive on disk")
			return
		}

		// Compute the sha256 checksum of the file.
		hash := sha256.New()
		buf = make([]byte, 1024*4)
		if _, err := io.CopyBuffer(hash, file, buf); err != nil {
			l.WithField("error", err).Error("failed to copy archive file for checksum verification")
			return
		}

		checksum := hex.EncodeToString(hash.Sum(nil))
		l.WithField("checksum", checksum).Info("computed checksum of transfer archive")

		// Verify the two checksums.
		if checksum != res.Header.Get("X-Checksum") {
			l.WithField("source_checksum", res.Header.Get("X-Checksum")).Error("checksum verification failed for archive")
			return
		}

		// Close the file.
		if err := file.Close(); err != nil {
			l.WithField("error", err).Error("failed to close archive file after calculating checksum")
			return
		}

		l.Info("server archive transfer checksums have been validated, creating server environment..")

		// Get the server data from the request.
		serverData, t, _, _ := jsonparser.Get(data, "server")
		if t != jsonparser.Object {
			l.Error("invalid server data passed in request")
			return
		}

		// Create a new server installer (note this does not execute the install script)
		i, err := installer.New(serverData)
		if err != nil {
			l.WithField("error", err).Error("failed to validate received server data")
			return
		}

		// Add the server to the collection.
		server.GetServers().Add(i.Server())

		// Create the server's environment (note this does not execute the install script)
		if err := i.Server().CreateEnvironment(); err != nil {
			l.WithField("error", err).Error("failed to create server environment")
			return
		}

		l.Info("server environment configured, extracting transfer archive..")
		// Extract the transfer archive.
		if err := archiver.NewTarGz().Unarchive(archivePath, i.Server().Filesystem().Path()); err != nil {
			l.WithField("error", err).Error("failed to extract server archive")
			return
		}

		// We mark the process as being successful here as if we fail to send a transfer success,
		// then a transfer failure won't probably be successful either.
		//
		// It may be useful to retry sending the transfer success every so often just in case of a small
		// hiccup or the fix of whatever error causing the success request to fail.
		hasError = false

		l.Info("server transfer archive has been extracted, notifying panel..")

		// Notify the panel that the transfer succeeded.
		err = api.New().SendTransferSuccess(serverID)
		if err != nil {
			if !api.IsRequestError(err) {
				l.WithField("error", err).Error("failed to notify panel of transfer success")
				return
			}

			l.WithField("error", err.Error()).Error("panel responded with error after transfer success")
			return
		}

		l.Info("successfully notified panel of transfer success")
	}(buf.Bytes())

	c.Status(http.StatusAccepted)
}
