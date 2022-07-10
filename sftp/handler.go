package sftp

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
)

const (
	PermissionFileRead        = "file.read"
	PermissionFileReadContent = "file.read-content"
	PermissionFileCreate      = "file.create"
	PermissionFileUpdate      = "file.update"
	PermissionFileDelete      = "file.delete"
)

type Handler struct {
	mu          sync.Mutex
	server      *server.Server
	fs          *filesystem.Filesystem
	events      *eventHandler
	permissions []string
	logger      *log.Entry
	ro          bool
}

// NewHandler returns a new connection handler for the SFTP server. This allows a given user
// to access the underlying filesystem.
func NewHandler(sc *ssh.ServerConn, srv *server.Server) (*Handler, error) {
	uuid, ok := sc.Permissions.Extensions["user"]
	if !ok {
		return nil, errors.New("sftp: mismatched Wings and Panel versions — Panel 1.10 is required for this version of Wings.")
	}

	events := eventHandler{
		ip:     sc.RemoteAddr().String(),
		user:   uuid,
		server: srv.ID(),
	}

	return &Handler{
		permissions: strings.Split(sc.Permissions.Extensions["permissions"], ","),
		server:      srv,
		fs:          srv.Filesystem(),
		events:      &events,
		ro:          config.Get().System.Sftp.ReadOnly,
		logger:      log.WithFields(log.Fields{"subsystem": "sftp", "user": uuid, "ip": sc.RemoteAddr()}),
	}, nil
}

// Handlers returns the sftp.Handlers for this struct.
func (h *Handler) Handlers() sftp.Handlers {
	return sftp.Handlers{
		FileGet:  h,
		FilePut:  h,
		FileCmd:  h,
		FileList: h,
	}
}

// Fileread creates a reader for a file on the system and returns the reader back.
func (h *Handler) Fileread(request *sftp.Request) (io.ReaderAt, error) {
	// Check first if the user can actually open and view a file. This permission is named
	// really poorly, but it is checking if they can read. There is an addition permission,
	// "save-files" which determines if they can write that file.
	if !h.can(PermissionFileReadContent) {
		return nil, sftp.ErrSSHFxPermissionDenied
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	f, _, err := h.fs.File(request.Filepath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			h.logger.WithField("error", err).Error("error processing readfile request")
			return nil, sftp.ErrSSHFxFailure
		}
		return nil, sftp.ErrSSHFxNoSuchFile
	}
	return f, nil
}

// Filewrite handles the write actions for a file on the system.
func (h *Handler) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	if h.ro {
		return nil, sftp.ErrSSHFxOpUnsupported
	}
	l := h.logger.WithField("source", request.Filepath)
	// If the user doesn't have enough space left on the server it should respond with an
	// error since we won't be letting them write this file to the disk.
	if !h.fs.HasSpaceAvailable(true) {
		return nil, ErrSSHQuotaExceeded
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	// The specific permission required to perform this action. If the file exists on the
	// system already it only needs to be an update, otherwise we'll check for a create.
	permission := PermissionFileUpdate
	_, sterr := h.fs.Stat(request.Filepath)
	if sterr != nil {
		if !errors.Is(sterr, os.ErrNotExist) {
			l.WithField("error", sterr).Error("error while getting file reader")
			return nil, sftp.ErrSSHFxFailure
		}
		permission = PermissionFileCreate
	}
	// Confirm the user has permission to perform this action BEFORE calling Touch, otherwise
	// you'll potentially create a file on the system and then fail out because of user
	// permission checking after the fact.
	if !h.can(permission) {
		return nil, sftp.ErrSSHFxPermissionDenied
	}
	f, err := h.fs.Touch(request.Filepath, os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		l.WithField("flags", request.Flags).WithField("error", err).Error("failed to open existing file on system")
		return nil, sftp.ErrSSHFxFailure
	}
	// Chown may or may not have been called in the touch function, so always do
	// it at this point to avoid the file being improperly owned.
	_ = h.fs.Chown(request.Filepath)
	event := server.ActivitySftpWrite
	if permission == PermissionFileCreate {
		event = server.ActivitySftpCreate
	}
	h.events.MustLog(event, FileAction{Entity: request.Filepath})
	return f, nil
}

// Filecmd hander for basic SFTP system calls related to files, but not anything to do with reading
// or writing to those files.
func (h *Handler) Filecmd(request *sftp.Request) error {
	if h.ro {
		return sftp.ErrSSHFxOpUnsupported
	}
	l := h.logger.WithField("source", request.Filepath)
	if request.Target != "" {
		l = l.WithField("target", request.Target)
	}

	switch request.Method {
	// Allows a user to make changes to the permissions of a given file or directory
	// on their server using their SFTP client.
	case "Setstat":
		if !h.can(PermissionFileUpdate) {
			return sftp.ErrSSHFxPermissionDenied
		}
		mode := request.Attributes().FileMode().Perm()
		// If the client passes an invalid FileMode just use the default 0644.
		if mode == 0o000 {
			mode = os.FileMode(0o644)
		}
		// Force directories to be 0755.
		if request.Attributes().FileMode().IsDir() {
			mode = 0o755
		}
		if err := h.fs.Chmod(request.Filepath, mode); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return sftp.ErrSSHFxNoSuchFile
			}
			l.WithField("error", err).Error("failed to perform setstat on item")
			return sftp.ErrSSHFxFailure
		}
		break
	// Support renaming a file (aka Move).
	case "Rename":
		if !h.can(PermissionFileUpdate) {
			return sftp.ErrSSHFxPermissionDenied
		}
		if err := h.fs.Rename(request.Filepath, request.Target); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return sftp.ErrSSHFxNoSuchFile
			}
			l.WithField("error", err).Error("failed to rename file")
			return sftp.ErrSSHFxFailure
		}
		h.events.MustLog(server.ActivitySftpRename, FileAction{Entity: request.Filepath, Target: request.Target})
		break
	// Handle deletion of a directory. This will properly delete all of the files and
	// folders within that directory if it is not already empty (unlike a lot of SFTP
	// clients that must delete each file individually).
	case "Rmdir":
		if !h.can(PermissionFileDelete) {
			return sftp.ErrSSHFxPermissionDenied
		}
		p := filepath.Clean(request.Filepath)
		if err := h.fs.Delete(p); err != nil {
			l.WithField("error", err).Error("failed to remove directory")
			return sftp.ErrSSHFxFailure
		}
		h.events.MustLog(server.ActivitySftpDelete, FileAction{Entity: request.Filepath})
		return sftp.ErrSSHFxOk
	// Handle requests to create a new Directory.
	case "Mkdir":
		if !h.can(PermissionFileCreate) {
			return sftp.ErrSSHFxPermissionDenied
		}
		name := strings.Split(filepath.Clean(request.Filepath), "/")
		p := strings.Join(name[0:len(name)-1], "/")
		if err := h.fs.CreateDirectory(name[len(name)-1], p); err != nil {
			l.WithField("error", err).Error("failed to create directory")
			return sftp.ErrSSHFxFailure
		}
		h.events.MustLog(server.ActivitySftpCreateDirectory, FileAction{Entity: request.Filepath})
		break
	// Support creating symlinks between files. The source and target must resolve within
	// the server home directory.
	case "Symlink":
		if !h.can(PermissionFileCreate) {
			return sftp.ErrSSHFxPermissionDenied
		}
		source, err := h.fs.SafePath(request.Filepath)
		if err != nil {
			return sftp.ErrSSHFxNoSuchFile
		}
		target, err := h.fs.SafePath(request.Target)
		if err != nil {
			return sftp.ErrSSHFxNoSuchFile
		}
		if err := os.Symlink(source, target); err != nil {
			l.WithField("target", target).WithField("error", err).Error("failed to create symlink")
			return sftp.ErrSSHFxFailure
		}
		break
	// Called when deleting a file.
	case "Remove":
		if !h.can(PermissionFileDelete) {
			return sftp.ErrSSHFxPermissionDenied
		}
		if err := h.fs.Delete(request.Filepath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return sftp.ErrSSHFxNoSuchFile
			}
			l.WithField("error", err).Error("failed to remove a file")
			return sftp.ErrSSHFxFailure
		}
		h.events.MustLog(server.ActivitySftpDelete, FileAction{Entity: request.Filepath})
		return sftp.ErrSSHFxOk
	default:
		return sftp.ErrSSHFxOpUnsupported
	}

	target := request.Filepath
	if request.Target != "" {
		target = request.Target
	}
	// Not failing here is intentional. We still made the file, it is just owned incorrectly
	// and will likely cause some issues. There is no logical check for if the file was removed
	// because both of those cases (Rmdir, Remove) have an explicit return rather than break.
	if err := h.fs.Chown(target); err != nil {
		l.WithField("error", err).Warn("error chowning file")
	}

	return sftp.ErrSSHFxOk
}

// Filelist is the handler for SFTP filesystem list calls. This will handle calls to list the contents of
// a directory as well as perform file/folder stat calls.
func (h *Handler) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	if !h.can(PermissionFileRead) {
		return nil, sftp.ErrSSHFxPermissionDenied
	}

	switch request.Method {
	case "List":
		p, err := h.fs.SafePath(request.Filepath)
		if err != nil {
			return nil, sftp.ErrSSHFxNoSuchFile
		}
		files, err := ioutil.ReadDir(p)
		if err != nil {
			h.logger.WithField("source", request.Filepath).WithField("error", err).Error("error while listing directory")
			return nil, sftp.ErrSSHFxFailure
		}
		return ListerAt(files), nil
	case "Stat":
		st, err := h.fs.Stat(request.Filepath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, sftp.ErrSSHFxNoSuchFile
			}
			h.logger.WithField("source", request.Filepath).WithField("error", err).Error("error performing stat on file")
			return nil, sftp.ErrSSHFxFailure
		}
		return ListerAt([]os.FileInfo{st.FileInfo}), nil
	default:
		return nil, sftp.ErrSSHFxOpUnsupported
	}
}

// Determines if a user has permission to perform a specific action on the SFTP server. These
// permissions are defined and returned by the Panel API.
func (h *Handler) can(permission string) bool {
	if h.server.IsSuspended() {
		return false
	}
	for _, p := range h.permissions {
		// If we match the permission specifically, or the user has been granted the "*"
		// permission because they're an admin, let them through.
		if p == permission || p == "*" {
			return true
		}
	}
	return false
}
