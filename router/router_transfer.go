package router

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"github.com/apex/log"
	"github.com/buger/jsonparser"
	"github.com/gin-gonic/gin"
	"github.com/mholt/archiver/v3"
	"github.com/pkg/errors"
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
		TrackedError(err).AbortWithServerError(c)
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
		if !os.IsNotExist(err) {
			TrackedServerError(err, s).SetMessage("failed to stat archive").AbortWithServerError(c)
			return
		}

		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	checksum, err := s.Archiver.Checksum()
	if err != nil {
		TrackedServerError(err, s).SetMessage("failed to calculate checksum").AbortWithServerError(c)
		return
	}

	file, err := os.Open(s.Archiver.ArchivePath())
	if err != nil {
		tserr := TrackedServerError(err, s)
		if !os.IsNotExist(err) {
			tserr.SetMessage("failed to open archive for reading")
		} else {
			tserr.SetMessage("failed to open archive")
		}

		tserr.AbortWithServerError(c)
		return
	}
	defer file.Close()

	c.Header("X-Checksum", checksum)
	c.Header("X-Mime-Type", st.Mimetype)
	c.Header("Content-Length", strconv.Itoa(int(st.Info.Size())))
	c.Header("Content-Disposition", "attachment; filename="+s.Archiver.ArchiveName())
	c.Header("Content-Type", "application/octet-stream")

	bufio.NewReader(file).WriteTo(c.Writer)
}

func postServerArchive(c *gin.Context) {
	s := GetServer(c.Param("server"))

	go func(s *server.Server) {
		if err := s.Archiver.Archive(); err != nil {
			s.Log().WithField("error", err).Error("failed to get archive for server")
			return
		}

		s.Log().Debug("successfully created server archive, notifying panel")

		r := api.NewRequester()
		rerr, err := r.SendArchiveStatus(s.Id(), true)
		if rerr != nil || err != nil {
			if err != nil {
				s.Log().WithField("error", err).Error("failed to notify panel of archive status")
				return
			}

			s.Log().WithField("error", rerr.String()).Error("panel returned an error when sending the archive status")

			return
		}

		s.Log().Debug("successfully notified panel of archive status")
	}(s)

	c.Status(http.StatusAccepted)
}

func postTransfer(c *gin.Context) {
	buf := bytes.Buffer{}
	buf.ReadFrom(c.Request.Body)

	go func(data []byte) {
		serverID, _ := jsonparser.GetString(data, "server_id")
		url, _ := jsonparser.GetString(data, "url")
		token, _ := jsonparser.GetString(data, "token")

		l := log.WithField("server", serverID)
		// Create an http client with no timeout.
		client := &http.Client{Timeout: 0}

		hasError := true
		defer func() {
			if !hasError {
				return
			}

			l.Info("server transfer failed, notifying panel")
			rerr, err := api.NewRequester().SendTransferFailure(serverID)
			if rerr != nil || err != nil {
				if err != nil {
					l.WithField("error", err).Error("failed to notify panel with transfer failure")
					return
				}

				l.WithField("error", errors.WithStack(rerr)).Error("received error response from panel while notifying of transfer failure")
				return
			}

			l.Debug("notified panel of transfer failure")
		}()

		// Make a new GET request to the URL the panel gave us.
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.WithField("error", errors.WithStack(err)).Error("failed to create http request for archive transfer")
			return
		}

		// Add the authorization header.
		req.Header.Set("Authorization", token)

		// Execute the http request.
		res, err := client.Do(req)
		if err != nil {
			l.WithField("error", errors.WithStack(err)).Error("failed to send archive http request")
			return
		}
		defer res.Body.Close()

		// Handle non-200 status codes.
		if res.StatusCode != 200 {
			_, err := ioutil.ReadAll(res.Body)
			if err != nil {
				l.WithField("error", errors.WithStack(err)).WithField("status", res.StatusCode).Error("failed read transfer response body")

				return
			}

			l.WithField("error", errors.WithStack(err)).WithField("status", res.StatusCode).Error("failed to request server archive")

			return
		}

		// Get the path to the archive.
		archivePath := filepath.Join(config.Get().System.ArchiveDirectory, serverID+".tar.gz")

		// Check if the archive already exists and delete it if it does.
		_, err = os.Stat(archivePath)
		if err != nil {
			if !os.IsNotExist(err) {
				l.WithField("error", errors.WithStack(err)).Error("failed to stat archive file")
				return
			}
		} else {
			if err := os.Remove(archivePath); err != nil {
				l.WithField("error", errors.WithStack(err)).Warn("failed to remove old archive file")

				return
			}
		}

		// Create the file.
		file, err := os.Create(archivePath)
		if err != nil {
			l.WithField("error", errors.WithStack(err)).Error("failed to open archive on disk")

			return
		}

		// Copy the file.
		buf := make([]byte, 1024*4)
		_, err = io.CopyBuffer(file, res.Body, buf)
		if err != nil {
			l.WithField("error", errors.WithStack(err)).Error("failed to copy archive file to disk")

			return
		}

		// Close the file so it can be opened to verify the checksum.
		if err := file.Close(); err != nil {
			l.WithField("error", errors.WithStack(err)).Error("failed to close archive file")

			return
		}

		l.WithField("server", serverID).Debug("server archive downloaded, computing checksum...")

		// Open the archive file for computing a checksum.
		file, err = os.Open(archivePath)
		if err != nil {
			l.WithField("error", errors.WithStack(err)).Error("failed to open archive on disk")
			return
		}

		// Compute the sha256 checksum of the file.
		hash := sha256.New()
		buf = make([]byte, 1024*4)
		if _, err := io.CopyBuffer(hash, file, buf); err != nil {
			l.WithField("error", errors.WithStack(err)).Error("failed to copy archive file for checksum verification")
			return
		}

		// Verify the two checksums.
		if hex.EncodeToString(hash.Sum(nil)) != res.Header.Get("X-Checksum") {
			l.Error("checksum verification failed for archive")
			return
		}

		// Close the file.
		if err := file.Close(); err != nil {
			l.WithField("error", errors.WithStack(err)).Error("failed to close archive file after calculating checksum")
			return
		}

		l.Info("server archive transfer was successful")

		// Get the server data from the request.
		serverData, t, _, _ := jsonparser.Get(data, "server")
		if t != jsonparser.Object {
			l.Error("invalid server data passed in request")
			return
		}

		// Create a new server installer (note this does not execute the install script)
		i, err := installer.New(serverData)
		if err != nil {
			l.WithField("error", errors.WithStack(err)).Error("failed to validate received server data")
			return
		}

		// Add the server to the collection.
		server.GetServers().Add(i.Server())

		// Create the server's environment (note this does not execute the install script)
		i.Execute()

		// Un-archive the archive. That sounds weird..
		if err := archiver.NewTarGz().Unarchive(archivePath, i.Server().Filesystem.Path()); err != nil {
			l.WithField("error", errors.WithStack(err)).Error("failed to extract server archive")
			return
		}

		// We mark the process as being successful here as if we fail to send a transfer success,
		// then a transfer failure won't probably be successful either.
		//
		// It may be useful to retry sending the transfer success every so often just in case of a small
		// hiccup or the fix of whatever error causing the success request to fail.
		hasError = false

		// Notify the panel that the transfer succeeded.
		rerr, err := api.NewRequester().SendTransferSuccess(serverID)
		if rerr != nil || err != nil {
			if err != nil {
				l.WithField("error", errors.WithStack(err)).Error("failed to notify panel of transfer success")
				return
			}

			l.WithField("error", errors.WithStack(rerr)).Error("panel responded with error after transfer success")

			return
		}

		l.Info("successfully notified panel of transfer success")
	}(buf.Bytes())

	c.Status(http.StatusAccepted)
}
