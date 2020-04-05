package main

import (
	"bufio"
	"github.com/gbrlsnchs/jwt/v3"
	"github.com/julienschmidt/httprouter"
	"github.com/pterodactyl/wings/config"
	"go.uber.org/zap"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Validates the provided JWT against the known secret for the Daemon and returns the
// parsed data.
func (rt *Router) parseBackupToken(token []byte) (*DownloadBackupPayload, error) {
	var payload DownloadBackupPayload
	if alg == nil {
		alg = jwt.NewHS256([]byte(config.Get().AuthenticationToken))
	}

	now := time.Now()
	verifyOptions := jwt.ValidatePayload(
		&payload.Payload,
		jwt.ExpirationTimeValidator(now),
	)

	_, err := jwt.Verify(token, alg, &payload, verifyOptions)
	if err != nil {
		return nil, err
	}

	return &payload, nil
}


func (rt *Router) routeDownloadBackup(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	defer r.Body.Close()

	payload, err := rt.parseBackupToken([]byte(r.URL.Query().Get("token")))
	// Some type of payload issue with the JWT, bail out here.
	if err != nil {
		zap.S().Warnw("failed to validate token for downloading backup", zap.Error(err))
		http.Error(w, "failed to open backup for download", http.StatusForbidden)

		return
	}

	store := getTokenStore()
	// The one-time-token in the payload is no longer valid, request is likely a repeat
	// so block it.
	if ok := store.IsValidToken(payload.UniqueId); !ok {
		http.NotFound(w, r)
		return
	}

	s := rt.GetServer(payload.ServerUuid)
	p, st, err := s.LocateBackup(payload.BackupUuid)
	if err != nil {
		if !os.IsNotExist(err) && !strings.HasPrefix(err.Error(), "invalid archive found") {
			zap.S().Warnw("failed to locate a backup for download", zap.String("path", p), zap.String("server", s.Uuid), zap.Error(err))
		}

		http.NotFound(w, r)
		return
	}

	f, err := os.OpenFile(p, os.O_RDONLY, 0)
	if err != nil {
		zap.S().Errorw("failed to open file for reading", zap.String("path", ps.ByName("path")), zap.String("server", s.Uuid), zap.Error(err))

		http.Error(w, "failed to open backup for download", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Length", strconv.Itoa(int(st.Size())))
	w.Header().Set("Content-Disposition", "attachment; filename="+st.Name())
	w.Header().Set("Content-Type", "application/octet-stream")

	bufio.NewReader(f).WriteTo(w)
}
