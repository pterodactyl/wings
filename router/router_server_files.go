package router

import (
	"bufio"
	"github.com/gin-gonic/gin"
	"net/http"
	"os"
	"strconv"
)

// Returns the contents of a file on the server.
func getServerFileContents(c *gin.Context) {
	s := GetServer(c.Param("server"))
	cleaned, err := s.Filesystem.SafePath(c.Query("file"))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The file requested could not be found.",
		})
		return
	}

	st, err := s.Filesystem.Stat(cleaned)
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	if st.Info.IsDir() {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource was not found on the system.",
		})
		return
	}

	f, err := os.Open(cleaned)
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}
	defer f.Close()

	c.Header("X-Mime-Type", st.Mimetype)
	c.Header("Content-Length", strconv.Itoa(int(st.Info.Size())))

	// If a download parameter is included in the URL go ahead and attach the necessary headers
	// so that the file can be downloaded.
	if c.Query("download") != "" {
		c.Header("Content-Disposition", "attachment; filename="+st.Info.Name())
		c.Header("Content-Type", "application/octet-stream")
	}

	bufio.NewReader(f).WriteTo(c.Writer)
}

// Returns the contents of a directory for a server.
func getServerListDirectory(c *gin.Context) {
	s := GetServer(c.Param("server"))

	stats, err := s.Filesystem.ListDirectory(c.Query("directory"))
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.JSON(http.StatusOK, stats)
}

// Renames (or moves) a file for a server.
func putServerRenameFile(c *gin.Context) {
	s := GetServer(c.Param("server"))

	var data struct{
		RenameFrom string `json:"rename_from"`
		RenameTo string `json:"rename_to"`
	}
	c.BindJSON(&data)

	if data.RenameFrom == "" || data.RenameTo == "" {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"error": "Invalid paths were provided, did you forget to provide both a new and old path?",
		})
		return
	}

	if err := s.Filesystem.Rename(data.RenameFrom, data.RenameTo); err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

// Copies a server file.
func postServerCopyFile(c *gin.Context) {
	s := GetServer(c.Param("server"))

	var data struct {
		Location string `json:"location"`
	}
	c.BindJSON(&data)

	if err := s.Filesystem.Copy(data.Location); err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

// Deletes a server file.
func postServerDeleteFile(c *gin.Context) {
	s := GetServer(c.Param("server"))

	var data struct {
		Location string `json:"location"`
	}
	c.BindJSON(&data)

	if err := s.Filesystem.Delete(data.Location); err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

// Writes the contents of the request to a file on a server.
func postServerWriteFile(c *gin.Context) {
	s := GetServer(c.Param("server"))

	if err := s.Filesystem.Writefile(c.Query("file"), c.Request.Body); err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

// Create a directory on a server.
func postServerCreateDirectory(c *gin.Context) {
	s := GetServer(c.Param("server"))

	var data struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	c.BindJSON(&data)

	if err := s.Filesystem.CreateDirectory(data.Name, data.Path); err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}