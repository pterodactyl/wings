package router

import (
	"bufio"
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/router/tokens"
	"net/http"
	"os"
	"strconv"
)

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

	p, st, err := s.LocateBackup(token.BackupUuid)
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	f, err := os.Open(p)
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
