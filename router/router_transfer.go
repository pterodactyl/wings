package router

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/buger/jsonparser"
	"github.com/gin-gonic/gin"
	"github.com/mholt/archiver/v3"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/installer"
	"github.com/pterodactyl/wings/router/tokens"
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
			"error": "( ..•˘___˘• .. )",
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

	go func(server *server.Server) {
		start := time.Now()

		if err := server.Archiver.Archive(); err != nil {
			zap.S().Errorw("failed to get archive for server", zap.String("server", s.Uuid), zap.Error(err))
			return
		}

		zap.S().Debugw(
			"successfully created archive for server",
			zap.String("server", server.Uuid),
			zap.Duration("time", time.Now().Sub(start).Round(time.Microsecond)),
		)

		r := api.NewRequester()
		rerr, err := r.SendArchiveStatus(server.Uuid, true)
		if rerr != nil || err != nil {
			if err != nil {
				zap.S().Errorw("failed to notify panel with archive status", zap.String("server", server.Uuid), zap.Error(err))
				return
			}

			zap.S().Errorw(
				"panel returned an error when sending the archive status",
				zap.String("server", server.Uuid),
				zap.Error(errors.New(rerr.String())),
			)
			return
		}

		zap.S().Debugw("successfully notified panel about archive status", zap.String("server", server.Uuid))
	}(s)

	c.Status(http.StatusAccepted)
}

func postTransfer(c *gin.Context) {
	zap.S().Debug("incoming transfer from panel")

	buf := bytes.Buffer{}
	buf.ReadFrom(c.Request.Body)

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
		if err := archiver.NewTarGz().Unarchive(archivePath, i.Server().Filesystem.Path()); err != nil {
			zap.S().Errorw("failed to extract archive", zap.String("server", serverID), zap.Error(err))
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
				zap.S().Errorw("failed to notify panel with transfer success", zap.String("server", serverID), zap.Error(err))
				return
			}

			zap.S().Errorw("panel returned an error when notifying of a transfer success", zap.String("server", serverID), zap.Error(errors.New(rerr.String())))
			return
		}

		zap.S().Debugw("successfully notified panel about transfer success", zap.String("server", serverID))
	}(buf.Bytes())

	c.Status(http.StatusAccepted)
}
