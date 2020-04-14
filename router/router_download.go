package router

import (
	"bufio"
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server/backup"
	"net/http"
	"os"
	"strconv"
)

// Handle a download request for a server backup.
func getDownloadBackup(c *gin.Context) {
	token := tokens.BackupPayload{}
	if err := tokens.ParseToken([]byte(c.Query("token")), &token); err != nil {
		TrackedError(err).AbortWithServerError(c)
		return
	}

	s := GetServer(token.ServerUuid)
	if s == nil || !token.IsUniqueRequest() {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource was not found on this server.",
		})
		return
	}

	b, st, err := backup.LocateLocal(token.BackupUuid)
	if err != nil {
		if os.IsNotExist(err) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "The requested backup was not found on this server.",
			})
			return
		}

		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	f, err := os.Open(b.Path())
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}
	defer f.Close()

	c.Header("Content-Length", strconv.Itoa(int(st.Size())))
	c.Header("Content-Disposition", "attachment; filename="+st.Name())
	c.Header("Content-Type", "application/octet-stream")

	bufio.NewReader(f).WriteTo(c.Writer)
}

// Handles downloading a specific file for a server.
func getDownloadFile(c *gin.Context) {
	token := tokens.FilePayload{}
	if err := tokens.ParseToken([]byte(c.Query("token")), &token); err != nil {
		TrackedError(err).AbortWithServerError(c)
		return
	}

	s := GetServer(token.ServerUuid)
	if s == nil || !token.IsUniqueRequest() {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource was not found on this server.",
		})
		return
	}

	p, _ := s.Filesystem.SafePath(token.FilePath)
	st, err := os.Stat(p)
	// If there is an error or we're somehow trying to download a directory, just
	// respond with the appropriate error.
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	} else if st.IsDir() {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource was not found on this server.",
		})
		return
	}

	f, err := os.Open(p)
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Header("Content-Length", strconv.Itoa(int(st.Size())))
	c.Header("Content-Disposition", "attachment; filename="+st.Name())
	c.Header("Content-Type", "application/octet-stream")

	bufio.NewReader(f).WriteTo(c.Writer)
}