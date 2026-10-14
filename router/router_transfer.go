package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/pterodactyl/wings/router/middleware"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/installer"
	"github.com/pterodactyl/wings/server/transfer"
)

// postTransfers .
func postTransfers(c *gin.Context) {
	auth := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
	if len(auth) != 2 || auth[0] != "Bearer" {
		c.Header("WWW-Authenticate", "Bearer")
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "The required authorization headers were not present in the request.",
		})
		return
	}

	token := tokens.TransferPayload{}
	if err := tokens.ParseToken([]byte(auth[1]), &token); err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	if !token.HasScope(tokens.ServerTransfer) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Forbidden."})
		return
	}
	middleware.ClearDeadlines(c)

	manager := middleware.ExtractManager(c)
	u, err := uuid.Parse(token.Subject)
	if err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	// Get or create a new transfer instance for this server.
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	// Only one request can send the server at a time.
	if transfer.Incoming().Get(u.String()) != nil {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "A transfer is already in progress for this server.",
		})
		return
	}

	var trnsfr *transfer.Transfer
	{
		// A server that already exists on this instance cannot be transferred to it.
		if _, ok := manager.Get(u.String()); ok {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"error": "A server with this identifier already exists on this instance.",
			})
			return
		}

		// TODO: should this use the request context?
		trnsfr = transfer.New(c, nil)

		ctx, cancel = context.WithCancel(trnsfr.Context())
		defer cancel()

		i, err := installer.New(ctx, manager, installer.ServerDetails{
			UUID:              u.String(),
			StartOnCompletion: false,
		})
		if err != nil {
			if err := manager.Client().SetTransferStatus(context.Background(), u.String(), false); err != nil {
				log.WithField("server", u.String()).WithField("status", false).WithError(err).Error("failed to set transfer status")
			}
			middleware.CaptureAndAbort(c, err)
			return
		}

		i.Server().SetTransferring(true)
		if !manager.AddIfMissing(i.Server()) {
			i.Server().CleanupForDestroy()
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"error": "A server with this identifier already exists on this instance.",
			})
			return
		}

		// We add the transfer to the list of transfers once we have a server instance to use.
		trnsfr.Server = i.Server()
		if !transfer.Incoming().Add(trnsfr) {
			manager.Remove(func(match *server.Server) bool { return match == i.Server() })
			i.Server().CleanupForDestroy()
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"error": "A transfer is already in progress for this server.",
			})
			return
		}
	}

	// Any errors past this point (until the transfer is complete) will abort
	// the transfer.

	successful := false
	defer func() {
		completeIncomingTransfer(manager, trnsfr, successful)
	}()

	mediaType, params, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		trnsfr.Log().Debug("failed to parse content type header")
		middleware.CaptureAndAbort(c, err)
		return
	}

	if !strings.HasPrefix(mediaType, "multipart/") {
		trnsfr.Log().Debug("invalid content type")
		middleware.CaptureAndAbort(c, fmt.Errorf("invalid content type \"%s\", expected \"multipart/form-data\"", mediaType))
		return
	}

	// Used to calculate the hash of the file as it is being uploaded.
	h := sha256.New()

	// Used to read the file and checksum from the request body.
	mr := multipart.NewReader(c.Request.Body, params["boundary"])

	// Loop through the parts of the request body and process them.
	var (
		hasArchive       bool
		hasChecksum      bool
		checksumVerified bool
	)
out:
	for {
		select {
		case <-ctx.Done():
			break out
		default:
			p, err := mr.NextPart()
			if err == io.EOF {
				break out
			}
			if err != nil {
				middleware.CaptureAndAbort(c, err)
				return
			}

			name := p.FormName()
			switch name {
			case "archive":
				trnsfr.Log().Debug("received archive")

				if err := trnsfr.Server.EnsureDataDirectoryExists(); err != nil {
					middleware.CaptureAndAbort(c, err)
					return
				}

				tee := io.TeeReader(p, h)
				if err := trnsfr.Server.Filesystem().ExtractTransfer(ctx, tee); err != nil {
					middleware.CaptureAndAbort(c, err)
					return
				}

				hasArchive = true
			case "checksum":
				trnsfr.Log().Debug("received checksum")

				if !hasArchive {
					middleware.CaptureAndAbort(c, errors.New("archive must be sent before the checksum"))
					return
				}

				hasChecksum = true

				// A SHA-256 checksum is 64 hexadecimal characters.
				v, err := io.ReadAll(io.LimitReader(p, 128))
				if err != nil {
					middleware.CaptureAndAbort(c, err)
					return
				}

				expected := make([]byte, hex.DecodedLen(len(v)))
				n, err := hex.Decode(expected, v)
				if err != nil {
					middleware.CaptureAndAbort(c, err)
					return
				}
				actual := h.Sum(nil)

				trnsfr.Log().WithFields(log.Fields{
					"expected": hex.EncodeToString(expected),
					"actual":   hex.EncodeToString(actual),
				}).Debug("checksums")

				if !bytes.Equal(expected[:n], actual) {
					middleware.CaptureAndAbort(c, errors.New("checksums don't match"))
					return
				}

				trnsfr.Log().Debug("checksums match")
				checksumVerified = true
			default:
				continue
			}
		}
	}

	if !hasArchive || !hasChecksum {
		middleware.CaptureAndAbort(c, errors.New("missing archive or checksum"))
		return
	}

	if !checksumVerified {
		middleware.CaptureAndAbort(c, errors.New("checksums don't match"))
		return
	}

	// Transfer is almost complete, we just want to ensure the environment is
	// configured correctly.  We might want to not fail the transfer at this
	// stage, but we will just to be safe.

	// Ensure the server environment gets configured.
	if err := trnsfr.Server.CreateEnvironment(); err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	// Changing this causes us to notify the panel about a successful transfer,
	// rather than failing the transfer like we do by default.
	successful = true

	// The rest of the logic for ensuring the server is unlocked and everything
	// is handled in the deferred function above.
	trnsfr.Log().Debug("done!")
}

// completeIncomingTransfer finishes receiving a transfer, telling the Panel
// whether it was successful. If it was not, everything that was received is
// removed, since the server still exists on the node it was being transferred
// from.
func completeIncomingTransfer(manager *server.Manager, trnsfr *transfer.Transfer, successful bool) {
	// Remove the transfer from the list of incoming transfers.
	transfer.Incoming().Remove(trnsfr)

	if !successful {
		trnsfr.Server.Events().Publish(server.TransferStatusEvent, "failure")
		manager.Remove(func(match *server.Server) bool {
			return match == trnsfr.Server
		})
		trnsfr.Server.CleanupForDestroy()
		if err := trnsfr.Server.Environment.Destroy(); err != nil {
			trnsfr.Log().WithError(err).Warn("failed to remove server environment")
		}
		if err := os.RemoveAll(trnsfr.Server.Filesystem().Path()); err != nil && !os.IsNotExist(err) {
			trnsfr.Log().WithError(err).Warn("failed to delete local server files")
		}
	}

	if err := manager.Client().SetTransferStatus(context.Background(), trnsfr.Server.ID(), successful); err != nil {
		trnsfr.Log().WithField("status", successful).WithError(err).Error("failed to set transfer status on panel")
	}

	if successful {
		trnsfr.Server.SetTransferring(false)
		trnsfr.Server.Events().Publish(server.TransferStatusEvent, "success")
	}
}

// deleteTransfer cancels an incoming transfer for a server.
func deleteTransfer(c *gin.Context) {
	trnsfr := transfer.Incoming().Get(c.Param("server"))
	if trnsfr == nil {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "Server is not currently being transferred.",
		})
		return
	}

	trnsfr.Cancel()

	c.Status(http.StatusAccepted)
}
