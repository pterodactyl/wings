package router

import (
	"context"
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/router/downloader"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
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
	s := ExtractServer(c)
	f, err := url.QueryUnescape(c.Query("file"))
	if err != nil {
		WithError(c, err)
		return
	}
	p := "/" + strings.TrimLeft(f, "/")
	st, err := s.Filesystem().Stat(p)
	if err != nil {
		WithError(c, err)
		return
	}

	c.Header("X-Mime-Type", st.Mimetype)
	c.Header("Content-Length", strconv.Itoa(int(st.Info.Size())))

	// If a download parameter is included in the URL go ahead and attach the necessary headers
	// so that the file can be downloaded.
	if c.Query("download") != "" {
		c.Header("Content-Disposition", "attachment; filename="+st.Info.Name())
		c.Header("Content-Type", "application/octet-stream")
	}

	// TODO(dane): should probably come up with a different approach here. If an error is encountered
	//  by this Readfile call you'll end up causing a (recovered) panic in the program because so many
	//  headers have already been set. We should probably add a RawReadfile that just returns the file
	//  to be read and then we can stream from that safely without error.
	//
	// Until that becomes a problem though I'm just going to leave this how it is. The panic is recovered
	// and a normal 500 error is returned to the client to my knowledge. It is also very unlikely to
	// happen since we're doing so much before this point that would normally throw an error if there
	// was a problem with the file.
	if err := s.Filesystem().Readfile(p, c.Writer); err != nil {
		WithError(c, err)
		return
	}
	c.Writer.Flush()
}

// Returns the contents of a directory for a server.
func getServerListDirectory(c *gin.Context) {
	s := ExtractServer(c)
	dir, err := url.QueryUnescape(c.Query("directory"))
	if err != nil {
		WithError(c, err)
		return
	}
	if stats, err := s.Filesystem().ListDirectory(dir); err != nil {
		WithError(c, err)
	} else {
		c.JSON(http.StatusOK, stats)
	}
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
				if err := s.Filesystem().Rename(pf, pt); err != nil {
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

		NewServerError(err, s).AbortFilesystemError(c)
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

	if err := s.Filesystem().Copy(data.Location); err != nil {
		NewServerError(err, s).AbortFilesystemError(c)
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
				return s.Filesystem().Delete(pi)
			}
		})
	}

	if err := g.Wait(); err != nil {
		NewServerError(err, s).Abort(c)
		return
	}

	c.Status(http.StatusNoContent)
}

// Writes the contents of the request to a file on a server.
func postServerWriteFile(c *gin.Context) {
	s := GetServer(c.Param("server"))

	f, err := url.QueryUnescape(c.Query("file"))
	if err != nil {
		NewServerError(err, s).Abort(c)
		return
	}
	f = "/" + strings.TrimLeft(f, "/")

	if err := s.Filesystem().Writefile(f, c.Request.Body); err != nil {
		if filesystem.IsErrorCode(err, filesystem.ErrCodeIsDirectory) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "Cannot write file, name conflicts with an existing directory by the same name.",
			})
			return
		}

		NewServerError(err, s).AbortFilesystemError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

// Returns all of the currently in-progress file downloads and their current download
// progress. The progress is also pushed out via a websocket event allowing you to just
// call this once to get current downloads, and then listen to targeted websocket events
// with the current progress for everything.
func getServerPullingFiles(c *gin.Context) {
	s := ExtractServer(c)
	c.JSON(http.StatusOK, gin.H{
		"downloads": downloader.ByServer(s.Id()),
	})
}

// Writes the contents of the remote URL to a file on a server.
func postServerPullRemoteFile(c *gin.Context) {
	s := ExtractServer(c)
	var data struct {
		URL       string `binding:"required" json:"url"`
		Directory string `binding:"required,omitempty" json:"directory"`
	}
	if err := c.BindJSON(&data); err != nil {
		return
	}

	u, err := url.Parse(data.URL)
	if err != nil {
		if e, ok := err.(*url.Error); ok {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "An error occurred while parsing that URL: " + e.Err.Error(),
			})
			return
		}
		WithError(c, err)
		return
	}

	if err := s.Filesystem().HasSpaceErr(true); err != nil {
		WithError(c, err)
		return
	}
	// Do not allow more than three simultaneous remote file downloads at one time.
	if len(downloader.ByServer(s.Id())) >= 3 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "This server has reached its limit of 3 simultaneous remote file downloads at once. Please wait for one to complete before trying again.",
		})
		return
	}

	dl := downloader.New(s, downloader.DownloadRequest{
		URL:       u,
		Directory: data.Directory,
	})

	// Execute this pull in a seperate thread since it may take a long time to complete.
	go func() {
		s.Log().WithField("download_id", dl.Identifier).WithField("url", u.String()).Info("starting pull of remote file to disk")
		if err := dl.Execute(); err != nil {
			s.Log().WithField("download_id", dl.Identifier).WithField("error", err).Error("failed to pull remote file")
		} else {
			s.Log().WithField("download_id", dl.Identifier).Info("completed pull of remote file")
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{
		"identifier": dl.Identifier,
	})
}

// Stops a remote file download if it exists and belongs to this server.
func deleteServerPullRemoteFile(c *gin.Context) {
	s := ExtractServer(c)
	if dl := downloader.ByID(c.Param("download")); dl != nil && dl.BelongsTo(s) {
		dl.Cancel()
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

	if err := s.Filesystem().CreateDirectory(data.Name, data.Path); err != nil {
		if err.Error() == "not a directory" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "Part of the path being created is not a directory (ENOTDIR).",
			})
			return
		}

		NewServerError(err, s).Abort(c)
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

	if !s.Filesystem().HasSpaceAvailable(true) {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "This server does not have enough available disk space to generate a compressed archive.",
		})
		return
	}

	f, err := s.Filesystem().CompressFiles(data.RootPath, data.Files)
	if err != nil {
		NewServerError(err, s).AbortFilesystemError(c)
		return
	}

	c.JSON(http.StatusOK, &filesystem.Stat{
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

	hasSpace, err := s.Filesystem().SpaceAvailableForDecompression(data.RootPath, data.File)
	if err != nil {
		// Handle an unknown format error.
		if filesystem.IsErrorCode(err, filesystem.ErrCodeUnknownArchive) {
			s.Log().WithField("error", err).Warn("failed to decompress file due to unknown format")
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "unknown archive format",
			})
			return
		}

		NewServerError(err, s).Abort(c)
		return
	}

	if !hasSpace {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "This server does not have enough available disk space to decompress this archive.",
		})
		return
	}

	if err := s.Filesystem().DecompressFile(data.RootPath, data.File); err != nil {
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

		NewServerError(err, s).AbortFilesystemError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

type chmodFile struct {
	File string `json:"file"`
	Mode string `json:"mode"`
}

var errInvalidFileMode = errors.New("invalid file mode")

func postServerChmodFile(c *gin.Context) {
	s := GetServer(c.Param("server"))

	var data struct {
		Root  string      `json:"root"`
		Files []chmodFile `json:"files"`
	}

	if err := c.BindJSON(&data); err != nil {
		log.Debug(err.Error())
		return
	}

	if len(data.Files) == 0 {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"error": "No files to chmod were provided.",
		})
		return
	}

	g, ctx := errgroup.WithContext(context.Background())

	// Loop over the array of files passed in and perform the move or rename action against each.
	for _, p := range data.Files {
		g.Go(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				mode, err := strconv.ParseUint(p.Mode, 8, 32)
				if err != nil {
					return errInvalidFileMode
				}

				if err := s.Filesystem().Chmod(path.Join(data.Root, p.File), os.FileMode(mode)); err != nil {
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
		if errors.Is(err, errInvalidFileMode) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "Invalid file mode.",
			})
			return
		}

		NewServerError(err, s).AbortFilesystemError(c)
		return
	}

	c.Status(http.StatusNoContent)
}

func postServerUploadFiles(c *gin.Context) {
	token := tokens.UploadPayload{}
	if err := tokens.ParseToken([]byte(c.Query("token")), &token); err != nil {
		NewTrackedError(err).Abort(c)
		return
	}

	s := GetServer(token.ServerUuid)
	if s == nil || !token.IsUniqueRequest() {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource was not found on this server.",
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

	var totalSize int64
	for _, header := range headers {
		totalSize += header.Size
	}

	for _, header := range headers {
		p, err := s.Filesystem().SafePath(filepath.Join(directory, header.Filename))
		if err != nil {
			NewServerError(err, s).AbortFilesystemError(c)
			return
		}

		// We run this in a different method so I can use defer without any of
		// the consequences caused by calling it in a loop.
		if err := handleFileUpload(p, s, header); err != nil {
			NewServerError(err, s).AbortFilesystemError(c)
			return
		}
	}
}

func handleFileUpload(p string, s *server.Server, header *multipart.FileHeader) error {
	file, err := header.Open()
	if err != nil {
		return err
	}
	defer file.Close()

	if err := s.Filesystem().Writefile(p, file); err != nil {
		return err
	}

	return nil
}
