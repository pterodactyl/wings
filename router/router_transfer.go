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
	"os"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server/filesystem"
	"github.com/pterodactyl/wings/server/transfer"
)

// postTransfers .
func postTransfers(c *gin.Context) {
	//auth := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
	//if len(auth) != 2 || auth[0] != "Bearer" {
	//	c.Header("WWW-Authenticate", "Bearer")
	//	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
	//		"error": "The required authorization heads were not present in the request.",
	//	})
	//	return
	//}
	//
	//token := tokens.TransferPayload{}
	//if err := tokens.ParseToken([]byte(auth[1]), &token); err != nil {
	//	NewTrackedError(err).Abort(c)
	//	return
	//}
	//
	//manager := middleware.ExtractManager(c)
	//u, err := uuid.Parse(token.Subject)
	//if err != nil {
	//	_ = WithError(c, err)
	//	return
	//}

	// TODO: replace, this is a temporary UUID for testing.
	u := uuid.MustParse("8798d797-474e-4f2f-a05c-595385d3c7c4")

	// Create a new transfer instance for this server.
	// TODO: should this use the request context?
	trnsfr := transfer.New(c, nil)
	//transfer.Incoming().Add(trnsfr)

	ctx, cancel := context.WithCancel(trnsfr.Context())
	defer cancel()

	//i, err := installer.New(ctx, manager, installer.ServerDetails{
	//	UUID:              u.String(),
	//	StartOnCompletion: false,
	//})
	//if err != nil {
	//	//_ = data.sendTransferStatus(manager.Client(), false)
	//	//data.log().WithField("error", err).Error("failed to validate received server data")
	//	_ = WithError(c, err)
	//	return
	//}
	//
	//trnsfr.Server = i.Server()

	fs := filesystem.New(filepath.Join(config.Get().System.Data, u.String()), 0, nil)

	mediaType, params, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		trnsfr.Log().Debug("failed to parse content type header")
		NewTrackedError(err).Abort(c)
		return
	}

	if !strings.HasPrefix(mediaType, "multipart/") {
		trnsfr.Log().Debug("invalid content type")
		NewTrackedError(fmt.Errorf("invalid content type \"%s\", expected \"multipart/form-data\"", mediaType)).Abort(c)
		return
	}

	// Used to calculate the hash of the file as it is being uploaded.
	h := sha256.New()

	mr := multipart.NewReader(c.Request.Body, params["boundary"])
out:
	for {
		select {
		case <-ctx.Done():
			break out
			// TODO: make sure default gets properly canceled.
		default:
			p, err := mr.NextPart()
			// TODO: Close the part?
			if err == io.EOF {
				break out
			}
			if err != nil {
				NewTrackedError(err).Abort(c)
				return
			}

			name := p.FormName()
			switch name {
			case "archive":
				trnsfr.Log().Debug("received archive")

				if _, err := os.Lstat(fs.Path()); err != nil {
					if os.IsNotExist(err) {
						_ = os.MkdirAll(fs.Path(), 0o700)
						_ = fs.Chown("/")
					}
				}

				tee := io.TeeReader(p, h)
				if err := fs.ExtractStreamUnsafe(ctx, "/", tee); err != nil {
					NewTrackedError(err).Abort(c)
					return
				}
			case "checksum":
				trnsfr.Log().Debug("received checksum")

				v, err := io.ReadAll(p)
				if err != nil {
					NewTrackedError(err).Abort(c)
					return
				}

				expected := make([]byte, hex.DecodedLen(len(v)))
				n, err := hex.Decode(expected, v)
				if err != nil {
					NewTrackedError(err).Abort(c)
					return
				}
				actual := h.Sum(nil)

				trnsfr.Log().WithFields(log.Fields{
					"expected": hex.EncodeToString(expected),
					"actual":   hex.EncodeToString(actual),
				}).Debug("checksums")

				if !bytes.Equal(expected[:n], actual) {
					NewTrackedError(errors.New("checksums don't match")).Abort(c)
					return
				}

				trnsfr.Log().Debug("checksums match")
			default:
				continue
			}
		}
	}

	trnsfr.Log().Debug("done")
}
