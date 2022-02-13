package router

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/juju/ratelimit"
	"github.com/mholt/archiver/v3"
	"github.com/mitchellh/colorstring"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/installer"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/middleware"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
	"github.com/pterodactyl/wings/system"
)

// Number of ticks in the progress bar
const ticks = 25

// 100% / number of ticks = percentage represented by each tick
const tickPercentage = 100 / ticks

type downloadProgress struct {
	size     int64
	progress int64
}

// Data passed over to initiate a server transfer.
type serverTransferRequest struct {
	ServerID string                  `binding:"required" json:"server_id"`
	URL      string                  `binding:"required" json:"url"`
	Token    string                  `binding:"required" json:"token"`
	Server   installer.ServerDetails `json:"server"`
}

func getArchivePath(sID string) string {
	return filepath.Join(config.Get().System.ArchiveDirectory, sID+".tar.gz")
}

// Returns the archive for a server so that it can be transferred to a new node.
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

	s := ExtractServer(c)
	if token.Subject != s.ID() {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "Missing required token subject, or subject is not valid for the requested server.",
		})
		return
	}

	archivePath := getArchivePath(s.ID())

	// Stat the archive file.
	st, err := os.Lstat(archivePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			_ = WithError(c, err)
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	// Compute sha1 checksum.
	h := sha256.New()
	f, err := os.Open(archivePath)
	if err != nil {
		return
	}
	if _, err := io.Copy(h, bufio.NewReader(f)); err != nil {
		_ = f.Close()
		_ = WithError(c, err)
		return
	}
	if err := f.Close(); err != nil {
		_ = WithError(c, err)
		return
	}
	checksum := hex.EncodeToString(h.Sum(nil))

	// Stream the file to the client.
	f, err = os.Open(archivePath)
	if err != nil {
		_ = WithError(c, err)
		return
	}
	defer f.Close()

	c.Header("X-Checksum", checksum)
	c.Header("X-Mime-Type", "application/tar+gzip")
	c.Header("Content-Length", strconv.Itoa(int(st.Size())))
	c.Header("Content-Disposition", "attachment; filename="+strconv.Quote(s.ID()+".tar.gz"))
	c.Header("Content-Type", "application/octet-stream")

	_, _ = bufio.NewReader(f).WriteTo(c.Writer)
}

func postServerArchive(c *gin.Context) {
	s := middleware.ExtractServer(c)
	manager := middleware.ExtractManager(c)

	go func(s *server.Server) {
		l := log.WithField("server", s.ID())

		// This function automatically adds the Source Node prefix and Timestamp to the log
		// output before sending it over the websocket.
		sendTransferLog := func(data string) {
			output := colorstring.Color(fmt.Sprintf("[yellow][bold]%s [Pterodactyl Transfer System] [Source Node]:[default] %s", time.Now().Format(time.RFC1123), data))
			s.Events().Publish(server.TransferLogsEvent, output)
		}

		s.Events().Publish(server.TransferStatusEvent, "starting")
		sendTransferLog("Attempting to archive server...")

		hasError := true
		defer func() {
			if !hasError {
				return
			}

			// Mark the server as not being transferred so it can actually be used.
			s.SetTransferring(false)
			s.Events().Publish(server.TransferStatusEvent, "failure")

			sendTransferLog("Attempting to notify panel of archive failure..")
			if err := manager.Client().SetArchiveStatus(s.Context(), s.ID(), false); err != nil {
				if !remote.IsRequestError(err) {
					sendTransferLog("Failed to notify panel of archive failure: " + err.Error())
					l.WithField("error", err).Error("failed to notify panel of failed archive status")
					return
				}

				sendTransferLog("Panel returned an error while notifying it of a failed archive: " + err.Error())
				l.WithField("error", err.Error()).Error("panel returned an error when notifying it of a failed archive status")
				return
			}

			sendTransferLog("Successfully notified panel of failed archive status")
			l.Info("successfully notified panel of failed archive status")
		}()

		// Mark the server as transferring to prevent problems.
		s.SetTransferring(true)

		// Ensure the server is offline. Sometimes a "No such container" error gets through
		// which means the server is already stopped. We can ignore that.
		if err := s.Environment.WaitForStop(s.Context(), time.Minute, false); err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such container") {
			sendTransferLog("Failed to stop server, aborting transfer..")
			l.WithField("error", err).Error("failed to stop server")
			return
		}

		// Create an archive of the entire server's data directory.
		a := &filesystem.Archive{
			BasePath: s.Filesystem().Path(),
		}

		// Attempt to get an archive of the server.
		if err := a.Create(getArchivePath(s.ID())); err != nil {
			sendTransferLog("An error occurred while archiving the server: " + err.Error())
			l.WithField("error", err).Error("failed to get transfer archive for server")
			return
		}

		sendTransferLog("Successfully created archive, attempting to notify panel..")
		l.Info("successfully created server transfer archive, notifying panel..")

		if err := manager.Client().SetArchiveStatus(s.Context(), s.ID(), true); err != nil {
			if !remote.IsRequestError(err) {
				sendTransferLog("Failed to notify panel of archive success: " + err.Error())
				l.WithField("error", err).Error("failed to notify panel of successful archive status")
				return
			}

			sendTransferLog("Panel returned an error while notifying it of a successful archive: " + err.Error())
			l.WithField("error", err.Error()).Error("panel returned an error when notifying it of a successful archive status")
			return
		}

		hasError = false

		// This log may not be displayed by the client due to the status event being sent before or at the same time.
		sendTransferLog("Successfully notified panel of successful archive status")

		l.Info("successfully notified panel of successful transfer archive status")
		s.Events().Publish(server.TransferStatusEvent, "archived")
	}(s)

	c.Status(http.StatusAccepted)
}

func (w *downloadProgress) Write(v []byte) (int, error) {
	n := len(v)
	atomic.AddInt64(&w.progress, int64(n))
	return n, nil
}

// Log helper function to attach all errors and info output to a consistently formatted
// log string for easier querying.
func (str serverTransferRequest) log() *log.Entry {
	return log.WithField("subsystem", "transfers").WithField("server_id", str.ServerID)
}

// Downloads an archive from the machine that the server currently lives on.
func (str serverTransferRequest) downloadArchive() (*http.Response, error) {
	client := http.Client{Timeout: 0}
	req, err := http.NewRequest(http.MethodGet, str.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", str.Token)
	res, err := client.Do(req) // lgtm [go/request-forgery]
	if err != nil {
		return nil, err
	}
	return res, nil
}

// Returns the path to the local archive on the system.
func (str serverTransferRequest) path() string {
	return getArchivePath(str.ServerID)
}

// Creates the archive location on this machine by first checking that the required file
// does not already exist. If it does exist, the file is deleted and then re-created as
// an empty file.
func (str serverTransferRequest) createArchiveFile() (*os.File, error) {
	p := str.path()
	if _, err := os.Stat(p); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else if err := os.Remove(p); err != nil {
		return nil, err
	}
	return os.Create(p)
}

// Deletes the archive from the local filesystem. This is executed as a deferred function.
func (str serverTransferRequest) removeArchivePath() {
	p := str.path()
	str.log().Debug("deleting temporary transfer archive")
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		str.log().WithField("path", p).WithField("error", err).Error("failed to delete temporary transfer archive file")
		return
	}
	str.log().Debug("deleted temporary transfer archive successfully")
}

// Verifies that the SHA-256 checksum of the file on the local filesystem matches the
// expected value from the transfer request. The string value returned is the computed
// checksum on the system.
func (str serverTransferRequest) verifyChecksum(matches string) (bool, string, error) {
	f, err := os.Open(str.path())
	if err != nil {
		return false, "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, bufio.NewReader(f)); err != nil {
		return false, "", err
	}
	checksum := hex.EncodeToString(h.Sum(nil))
	return checksum == matches, checksum, nil
}

// Sends a notification to the Panel letting it know what the status of this transfer is.
func (str serverTransferRequest) sendTransferStatus(client remote.Client, successful bool) error {
	lg := str.log().WithField("transfer_successful", successful)
	lg.Info("notifying Panel of server transfer state")
	if err := client.SetTransferStatus(context.Background(), str.ServerID, successful); err != nil {
		lg.WithField("error", err).Error("error notifying panel of transfer state")
		return err
	}
	lg.Debug("notified panel of transfer state")
	return nil
}

// Initiates a transfer between two nodes for a server by downloading an archive from the
// remote node and then applying the server details to this machine.
func postTransfer(c *gin.Context) {
	var data serverTransferRequest
	if err := c.BindJSON(&data); err != nil {
		return
	}

	manager := middleware.ExtractManager(c)
	u, err := uuid.Parse(data.ServerID)
	if err != nil {
		WithError(c, err)
		return
	}
	// Force the server ID to be a valid UUID string at this point. If it is not an error
	// is returned to the caller. This limits injection vulnerabilities that would cause
	// the str.path() function to return a location not within the server archive directory.
	data.ServerID = u.String()

	data.log().Info("handling incoming server transfer request")
	go func(data *serverTransferRequest) {
		hasError := true

		// Create a new server installer. This will only configure the environment and not
		// run the installer scripts.
		i, err := installer.New(context.Background(), manager, data.Server)
		if err != nil {
			_ = data.sendTransferStatus(manager.Client(), false)
			data.log().WithField("error", err).Error("failed to validate received server data")
			return
		}

		// This function automatically adds the Target Node prefix and Timestamp to the log output before sending it
		// over the websocket.
		sendTransferLog := func(data string) {
			output := colorstring.Color(fmt.Sprintf("[yellow][bold]%s [Pterodactyl Transfer System] [Target Node]:[default] %s", time.Now().Format(time.RFC1123), data))
			i.Server().Events().Publish(server.TransferLogsEvent, output)
		}

		// Mark the server as transferring to prevent problems later on during the process and
		// then push the server into the global server collection for this instance.
		i.Server().SetTransferring(true)
		manager.Add(i.Server())
		defer func(s *server.Server) {
			// In the event that this transfer call fails, remove the server from the global
			// server tracking so that we don't have a dangling instance.
			if err := data.sendTransferStatus(manager.Client(), !hasError); hasError || err != nil {
				sendTransferLog("Server transfer failed, check Wings logs for additional information.")
				s.Events().Publish(server.TransferStatusEvent, "failure")
				manager.Remove(func(match *server.Server) bool {
					return match.ID() == s.ID()
				})

				// If the transfer status was successful but the request failed, act like the transfer failed.
				if !hasError && err != nil {
					// Delete all extracted files.
					if err := os.RemoveAll(s.Filesystem().Path()); err != nil && !os.IsNotExist(err) {
						data.log().WithField("error", err).Warn("failed to delete local server files directory")
					}
				}
			} else {
				s.SetTransferring(false)
				s.Events().Publish(server.TransferStatusEvent, "success")
				sendTransferLog("Transfer completed.")
			}
		}(i.Server())

		data.log().Info("downloading server archive from current server node")
		sendTransferLog("Received incoming transfer from Panel, attempting to download archive from source node...")
		res, err := data.downloadArchive()
		if err != nil {
			sendTransferLog("Failed to retrieve server archive from remote node: " + err.Error())
			data.log().WithField("error", err).Error("failed to download archive for server transfer")
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			data.log().WithField("error", err).WithField("status", res.StatusCode).Error("unexpected error response from transfer endpoint")
			return
		}

		size := res.ContentLength
		if size == 0 {
			data.log().WithField("error", err).Error("received an archive response with Content-Length of 0")
			return
		}
		sendTransferLog("Got server archive response from remote node. (Content-Length: " + strconv.Itoa(int(size)) + ")")
		sendTransferLog("Creating local archive file...")
		file, err := data.createArchiveFile()
		if err != nil {
			data.log().WithField("error", err).Error("failed to create archive file on local filesystem")
			return
		}

		sendTransferLog("Writing archive to disk...")
		data.log().Info("writing transfer archive to disk...")

		// Copy the file.
		progress := &downloadProgress{size: size}
		ticker := time.NewTicker(3 * time.Second)
		go func(progress *downloadProgress, t *time.Ticker) {
			for range ticker.C {
				// p = 100 (Downloaded)
				// size = 1000 (Content-Length)
				// p / size = 0.1
				// * 100 = 10% (Multiply by 100 to get a percentage of the download)
				// 10% / tickPercentage = (10% / (100 / 25)) (Divide by tick percentage to get the number of ticks)
				// 2.5 (Number of ticks as a float64)
				// 2 (convert to an integer)
				p := atomic.LoadInt64(&progress.progress)
				// We have to cast these numbers to float in order to get a float result from the division.
				width := ((float64(p) / float64(size)) * 100) / tickPercentage
				bar := strings.Repeat("=", int(width)) + strings.Repeat(" ", ticks-int(width))
				sendTransferLog("Downloading [" + bar + "] " + system.FormatBytes(p) + " / " + system.FormatBytes(progress.size))
			}
		}(progress, ticker)

		var reader io.Reader
		downloadLimit := float64(config.Get().System.Transfers.DownloadLimit) * 1024 * 1024
		if downloadLimit > 0 {
			// Wrap the body with a reader that is limited to the defined download limit speed.
			reader = ratelimit.Reader(res.Body, ratelimit.NewBucketWithRate(downloadLimit, int64(downloadLimit)))
		} else {
			reader = res.Body
		}

		buf := make([]byte, 1024*4)
		if _, err := io.CopyBuffer(file, io.TeeReader(reader, progress), buf); err != nil {
			ticker.Stop()
			_ = file.Close()

			sendTransferLog("Failed while writing archive file to disk: " + err.Error())
			data.log().WithField("error", err).Error("failed to copy archive file to disk")
			return
		}
		ticker.Stop()

		// Show 100% completion.
		humanSize := system.FormatBytes(progress.size)
		sendTransferLog("Downloading [" + strings.Repeat("=", ticks) + "] " + humanSize + " / " + humanSize)

		if err := file.Close(); err != nil {
			data.log().WithField("error", err).Error("unable to close archive file on local filesystem")
			return
		}
		data.log().Info("finished writing transfer archive to disk")
		sendTransferLog("Successfully wrote archive to disk.")

		// Whenever the transfer fails or succeeds, delete the temporary transfer archive that
		// was created on the disk.
		defer data.removeArchivePath()

		sendTransferLog("Verifying checksum of downloaded archive...")
		data.log().Info("computing checksum of downloaded archive file")
		expected := res.Header.Get("X-Checksum")
		if matches, computed, err := data.verifyChecksum(expected); err != nil {
			data.log().WithField("error", err).Error("encountered an error while calculating local filesystem archive checksum")
			return
		} else if !matches {
			sendTransferLog("@@@@@ CHECKSUM VERIFICATION FAILED @@@@@")
			sendTransferLog("  -   Source Checksum: " + expected)
			sendTransferLog("  - Computed Checksum: " + computed)
			data.log().WithField("expected_sum", expected).WithField("computed_checksum", computed).Error("checksum mismatch when verifying integrity of local archive")
			return
		}

		// Create the server's environment.
		sendTransferLog("Creating server environment, this could take a while..")
		data.log().Info("creating server environment")
		if err := i.Server().CreateEnvironment(); err != nil {
			data.log().WithField("error", err).Error("failed to create server environment")
			return
		}

		sendTransferLog("Server environment has been created, extracting transfer archive..")
		data.log().Info("server environment configured, extracting transfer archive")
		if err := archiver.NewTarGz().Unarchive(data.path(), i.Server().Filesystem().Path()); err != nil {
			// Un-archiving failed, delete the server's data directory.
			if err := os.RemoveAll(i.Server().Filesystem().Path()); err != nil && !os.IsNotExist(err) {
				data.log().WithField("error", err).Warn("failed to delete local server files directory")
			}
			data.log().WithField("error", err).Error("failed to extract server archive")
			return
		}

		// We mark the process as being successful here as if we fail to send a transfer success,
		// then a transfer failure won't probably be successful either.
		//
		// It may be useful to retry sending the transfer success every so often just in case of a small
		// hiccup or the fix of whatever error causing the success request to fail.
		hasError = false

		data.log().Info("archive extracted successfully, notifying Panel of status")
		sendTransferLog("Archive extracted successfully.")
	}(&data)

	c.Status(http.StatusAccepted)
}
