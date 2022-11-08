package router

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/server/installer"
	"github.com/pterodactyl/wings/server/transfer"
)

// Data passed over to initiate a server transfer.
type serverTransferRequest struct {
	URL    string                  `binding:"required" json:"url"`
	Token  string                  `binding:"required" json:"token"`
	Server installer.ServerDetails `json:"server"`
}

// postServerTransfer handles the start of a transfer for a server.
func postServerTransfer(c *gin.Context) {
	var data serverTransferRequest
	if err := c.BindJSON(&data); err != nil {
		return
	}

	s := ExtractServer(c)

	// Check if the server is already being transferred.
	// There will be another endpoint for resetting this value either by deleting the
	// server, or by canceling the transfer.
	if s.IsTransferring() {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "A transfer is already in progress for this server.",
		})
		return
	}

	// Block the server from starting while we are transferring it.
	s.SetTransferring(true)

	// Ensure the server is offline. Sometimes a "No such container" error gets through
	// which means the server is already stopped. We can ignore that.
	if s.Environment.State() != environment.ProcessOfflineState {
		if err := s.Environment.WaitForStop(
			s.Context(),
			time.Minute,
			false,
		); err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such container") {
			s.SetTransferring(false)
			s.Log().WithError(err).Error("failed to stop server for transfer")
			return
		}
	}

	// Create a new transfer instance for this server.
	trnsfr := transfer.New(context.Background(), s)
	transfer.Outgoing().Add(trnsfr)

	go func() {
		_, err := trnsfr.PushArchiveToTarget(data.URL, data.Token)
		if err != nil {
			if err == context.Canceled {
				trnsfr.Log().Debug("canceled")
				trnsfr.SendSourceMessage("Canceled.")
				return
			}

			trnsfr.Log().WithError(err).Error("failed to push archive to target")
			return
		}

		//log.Debug(string(v))
	}()

	c.Status(http.StatusAccepted)
}

// deleteServerTransfer cancels a transfer for a server.
func deleteServerTransfer(c *gin.Context) {
	s := ExtractServer(c)

	if !s.IsTransferring() {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "Server is not currently being transferred.",
		})
		return
	}

	trnsfr := transfer.Outgoing().Get(s.ID())
	if trnsfr == nil {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "Server is not currently being transferred.",
		})
		return
	}

	trnsfr.Cancel()

	// TODO: Do we want to wait here until we are sure the transfer has been canceled?
}
