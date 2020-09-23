package router

import (
	"bufio"
	"context"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
	"golang.org/x/sync/errgroup"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Returns the contents of a file on the server.
func getServerFileContents(c *gin.Context) {
	s := GetServer(c.Param("server"))

	p, err := url.QueryUnescape(c.Query("file"))
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}
	p = "/" + strings.TrimLeft(p, "/")

	cleaned, err := s.Filesystem.SafePath(p)
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

	d, err := url.QueryUnescape(c.Query("directory"))
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	stats, err := s.Filesystem.ListDirectory(d)
	if err != nil {
		if err.Error() == "readdirent: not a directory" {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "The requested directory does not exist.",
			})
			return
		}

		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.JSON(http.StatusOK, stats)
}

type renameFile struct {
	To   string `json:"to"`
	From string `json:"from"`
}

// Renames (or moves) files for a server.
func putServerRenameFiles(c *gin.Context) {
	s := GetServer(c.Param("server"))

	var data struct {
		Root  string       `json:"root"`
		Files []renameFile `json:"files"`
	}
	// BindJSON sends 400 if the request fails, all we need to do is return
	if err := c.BindJSON(&data); err != nil {
		return
	}

	if len(data.Files) == 0 {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"error": "No files to move or rename were provided.",
		})
		return
	}

	g, ctx := errgroup.WithContext(context.Background())

	// Loop over the array of files passed in and perform the move or rename action against each.
	for _, p := range data.Files {
		pf := path.Join(data.Root, p.From)
		pt := path.Join(data.Root, p.To)

		g.Go(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				if err := s.Filesystem.Rename(pf, pt); err != nil {
					// Return nil if the error is an is not exists.
					// NOTE: os.IsNotExist() does not work if the error is wrapped.
					if errors.Is(err, os.ErrNotExist) {
						return nil
					}

					return err
				}

				return nil
			}
		})
	}

	if err := g.Wait(); err != nil {
		if errors.Is(err, os.ErrExist) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "Cannot move or rename file, destination already exists.",
			})
			return
		}

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
	// BindJSON sends 400 if the request fails, all we need to do is return
	if err := c.BindJSON(&data); err != nil {
		return
	}

	if err := s.Filesystem.Copy(data.Location); err != nil {
		// Check if the file does not exist.
		// NOTE: os.IsNotExist() does not work if the error is wrapped.
		if errors.Is(err, os.ErrNotExist) {
			c.Status(http.StatusNotFound)
			return
		}

		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

// Deletes files from a server.
func postServerDeleteFiles(c *gin.Context) {
	s := GetServer(c.Param("server"))

	var data struct {
		Root  string   `json:"root"`
		Files []string `json:"files"`
	}

	if err := c.BindJSON(&data); err != nil {
		return
	}

	if len(data.Files) == 0 {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"error": "No files were specified for deletion.",
		})
		return
	}

	g, ctx := errgroup.WithContext(context.Background())

	// Loop over the array of files passed in and delete them. If any of the file deletions
	// fail just abort the process entirely.
	for _, p := range data.Files {
		pi := path.Join(data.Root, p)

		g.Go(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return s.Filesystem.Delete(pi)
			}
		})
	}

	if err := g.Wait(); err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

// Writes the contents of the request to a file on a server.
func postServerWriteFile(c *gin.Context) {
	s := GetServer(c.Param("server"))

	f, err := url.QueryUnescape(c.Query("file"))
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}
	f = "/" + strings.TrimLeft(f, "/")

	if err := s.Filesystem.Writefile(f, c.Request.Body); err != nil {
		if errors.Is(err, server.ErrIsDirectory) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "Cannot write file, name conflicts with an existing directory by the same name.",
			})
			return
		}

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
	// BindJSON sends 400 if the request fails, all we need to do is return
	if err := c.BindJSON(&data); err != nil {
		return
	}

	if err := s.Filesystem.CreateDirectory(data.Name, data.Path); err != nil {
		if err.Error() == "not a directory" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "Part of the path being created is not a directory (ENOTDIR).",
			})
			return
		}

		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

func postServerCompressFiles(c *gin.Context) {
	s := GetServer(c.Param("server"))

	var data struct {
		RootPath string   `json:"root"`
		Files    []string `json:"files"`
	}

	if err := c.BindJSON(&data); err != nil {
		return
	}

	if len(data.Files) == 0 {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"error": "No files were passed through to be compressed.",
		})
		return
	}

	if !s.Filesystem.HasSpaceAvailable(true) {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "This server does not have enough available disk space to generate a compressed archive.",
		})
		return
	}

	f, err := s.Filesystem.CompressFiles(data.RootPath, data.Files)
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.JSON(http.StatusOK, &server.Stat{
		Info:     f,
		Mimetype: "application/tar+gzip",
	})
}

func postServerDecompressFiles(c *gin.Context) {
	s := GetServer(c.Param("server"))

	var data struct {
		RootPath string `json:"root"`
		File     string `json:"file"`
	}

	if err := c.BindJSON(&data); err != nil {
		return
	}

	hasSpace, err := s.Filesystem.SpaceAvailableForDecompression(data.RootPath, data.File)
	if err != nil {
		// Handle an unknown format error.
		if errors.Is(err, server.ErrUnknownArchiveFormat) {
			s.Log().WithField("error", err).Warn("failed to decompress file due to unknown format")

			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "unknown archive format",
			})
			return
		}

		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	if !hasSpace {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "This server does not have enough available disk space to decompress this archive.",
		})
		return
	}

	if err := s.Filesystem.DecompressFile(data.RootPath, data.File); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "The requested archive was not found.",
			})
			return
		}

		// If the file is busy for some reason just return a nicer error to the user since there is not
		// much we specifically can do. They'll need to stop the running server process in order to overwrite
		// a file like this.
		if strings.Contains(err.Error(), "text file busy") {
			s.Log().WithField("error", err).Warn("failed to decompress file due to busy text file")

			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "One or more files this archive is attempting to overwrite are currently in use by another process. Please try again.",
			})
			return
		}

		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

func postServerUploadFiles(c *gin.Context) {
	token := tokens.UploadPayload{}
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

	if !s.Filesystem.HasSpaceAvailable(true) {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "This server does not have enough available disk space to accept any file uploads.",
		})
		return
	}

	form, err := c.MultipartForm()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Failed to get multipart form data from request.",
		})
		return
	}

	headers, ok := form.File["files"]
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "No files were found on the request body.",
		})
		return
	}

	directory := c.Query("directory")

	for _, header := range headers {
		p, err := s.Filesystem.SafePath(filepath.Join(directory, header.Filename))
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}

		// We run this in a different method so I can use defer without any of
		// the consequences caused by calling it in a loop.
		if err := handleFileUpload(p, s, header); err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}
}

func handleFileUpload(p string, s *server.Server, header *multipart.FileHeader) error {
	file, err := header.Open()
	if err != nil {
		return errors.WithStack(err)
	}
	defer file.Close()

	if err := s.Filesystem.Writefile(p, file); err != nil {
		return errors.WithStack(err)
	}

	return nil
}
