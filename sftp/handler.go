package sftp

import (
	"github.com/apex/log"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/sftp"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

type FileSystem struct {
	UUID        string
	Permissions []string
	ReadOnly    bool
	User        User
	Cache       *cache.Cache

	PathValidator func(fs FileSystem, p string) (string, error)
	HasDiskSpace  func(fs FileSystem) bool

	logger *log.Entry
	lock   sync.Mutex
}

func (fs FileSystem) buildPath(p string) (string, error) {
	return fs.PathValidator(fs, p)
}

const (
	PermissionFileRead        = "file.read"
	PermissionFileReadContent = "file.read-content"
	PermissionFileCreate      = "file.create"
	PermissionFileUpdate      = "file.update"
	PermissionFileDelete      = "file.delete"
)

// Fileread creates a reader for a file on the system and returns the reader back.
func (fs FileSystem) Fileread(request *sftp.Request) (io.ReaderAt, error) {
	// Check first if the user can actually open and view a file. This permission is named
	// really poorly, but it is checking if they can read. There is an addition permission,
	// "save-files" which determines if they can write that file.
	if !fs.can(PermissionFileReadContent) {
		return nil, sftp.ErrSshFxPermissionDenied
	}

	p, err := fs.buildPath(request.Filepath)
	if err != nil {
		return nil, sftp.ErrSshFxNoSuchFile
	}

	fs.lock.Lock()
	defer fs.lock.Unlock()

	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil, sftp.ErrSshFxNoSuchFile
	} else if err != nil {
		fs.logger.WithField("error", err).Error("error while processing file stat")

		return nil, sftp.ErrSshFxFailure
	}

	file, err := os.Open(p)
	if err != nil {
		fs.logger.WithField("source", p).WithField("error", err).Error("could not open file for reading")
		return nil, sftp.ErrSshFxFailure
	}

	return file, nil
}

// Filewrite handles the write actions for a file on the system.
func (fs FileSystem) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	if fs.ReadOnly {
		return nil, sftp.ErrSshFxOpUnsupported
	}

	p, err := fs.buildPath(request.Filepath)
	if err != nil {
		return nil, sftp.ErrSshFxNoSuchFile
	}

	l := fs.logger.WithField("source", p)

	// If the user doesn't have enough space left on the server it should respond with an
	// error since we won't be letting them write this file to the disk.
	if !fs.HasDiskSpace(fs) {
		return nil, ErrSshQuotaExceeded
	}

	fs.lock.Lock()
	defer fs.lock.Unlock()

	stat, statErr := os.Stat(p)
	// If the file doesn't exist we need to create it, as well as the directory pathway
	// leading up to where that file will be created.
	if os.IsNotExist(statErr) {
		// This is a different pathway than just editing an existing file. If it doesn't exist already
		// we need to determine if this user has permission to create files.
		if !fs.can(PermissionFileCreate) {
			return nil, sftp.ErrSshFxPermissionDenied
		}

		// Create all of the directories leading up to the location where this file is being created.
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			l.WithFields(log.Fields{
				"path":  filepath.Dir(p),
				"error": err,
			}).Error("error making path for file")

			return nil, sftp.ErrSshFxFailure
		}

		file, err := os.Create(p)
		if err != nil {
			l.WithField("error", err).Error("failed to create file")

			return nil, sftp.ErrSshFxFailure
		}

		// Not failing here is intentional. We still made the file, it is just owned incorrectly
		// and will likely cause some issues.
		if err := os.Chown(p, fs.User.Uid, fs.User.Gid); err != nil {
			l.WithField("error", err).Warn("failed to set permissions on file")
		}

		return file, nil
	}

	// If the stat error isn't about the file not existing, there is some other issue
	// at play and we need to go ahead and bail out of the process.
	if statErr != nil {
		l.WithField("error", statErr).Error("encountered error performing file stat")

		return nil, sftp.ErrSshFxFailure
	}

	// If we've made it here it means the file already exists and we don't need to do anything
	// fancy to handle it. Just pass over the request flags so the system knows what the end
	// goal with the file is going to be.
	//
	// But first, check that the user has permission to save modified files.
	if !fs.can(PermissionFileUpdate) {
		return nil, sftp.ErrSshFxPermissionDenied
	}

	// Not sure this would ever happen, but lets not find out.
	if stat.IsDir() {
		return nil, sftp.ErrSshFxOpUnsupported
	}

	file, err := os.Create(p)
	if err != nil {
		// Prevent errors if the file is deleted between the stat and this call.
		if os.IsNotExist(err) {
			return nil, sftp.ErrSSHFxNoSuchFile
		}

		l.WithField("flags", request.Flags).WithField("error", err).Error("failed to open existing file on system")
		return nil, sftp.ErrSshFxFailure
	}

	// Not failing here is intentional. We still made the file, it is just owned incorrectly
	// and will likely cause some issues.
	if err := os.Chown(p, fs.User.Uid, fs.User.Gid); err != nil {
		l.WithField("error", err).Warn("error chowning file")
	}

	return file, nil
}

// Filecmd hander for basic SFTP system calls related to files, but not anything to do with reading
// or writing to those files.
func (fs FileSystem) Filecmd(request *sftp.Request) error {
	if fs.ReadOnly {
		return sftp.ErrSshFxOpUnsupported
	}

	p, err := fs.buildPath(request.Filepath)
	if err != nil {
		return sftp.ErrSshFxNoSuchFile
	}

	l := fs.logger.WithField("source", p)

	var target string
	// If a target is provided in this request validate that it is going to the correct
	// location for the server. If it is not, return an operation unsupported error. This
	// is maybe not the best error response, but its not wrong either.
	if request.Target != "" {
		target, err = fs.buildPath(request.Target)
		if err != nil {
			return sftp.ErrSshFxOpUnsupported
		}
	}

	switch request.Method {
	case "Setstat":
		if !fs.can(PermissionFileUpdate) {
			return sftp.ErrSshFxPermissionDenied
		}

		mode := os.FileMode(0644)
		// If the client passed a valid file permission use that, otherwise use the
		// default of 0644 set above.
		if request.Attributes().FileMode().Perm() != 0000 {
			mode = request.Attributes().FileMode().Perm()
		}

		// Force directories to be 0755
		if request.Attributes().FileMode().IsDir() {
			mode = 0755
		}

		if err := os.Chmod(p, mode); err != nil {
			if os.IsNotExist(err) {
				return sftp.ErrSSHFxNoSuchFile
			}

			l.WithField("error", err).Error("failed to perform setstat on item")
			return sftp.ErrSSHFxFailure
		}
		return nil
	case "Rename":
		if !fs.can(PermissionFileUpdate) {
			return sftp.ErrSSHFxPermissionDenied
		}

		if err := os.Rename(p, target); err != nil {
			if os.IsNotExist(err) {
				return sftp.ErrSSHFxNoSuchFile
			}

			l.WithField("target", target).WithField("error", err).Error("failed to rename file")

			return sftp.ErrSshFxFailure
		}

		break
	case "Rmdir":
		if !fs.can(PermissionFileDelete) {
			return sftp.ErrSshFxPermissionDenied
		}

		if err := os.RemoveAll(p); err != nil {
			l.WithField("error", err).Error("failed to remove directory")

			return sftp.ErrSshFxFailure
		}

		return sftp.ErrSshFxOk
	case "Mkdir":
		if !fs.can(PermissionFileCreate) {
			return sftp.ErrSshFxPermissionDenied
		}

		if err := os.MkdirAll(p, 0755); err != nil {
			l.WithField("error", err).Error("failed to create directory")

			return sftp.ErrSshFxFailure
		}

		break
	case "Symlink":
		if !fs.can(PermissionFileCreate) {
			return sftp.ErrSshFxPermissionDenied
		}

		if err := os.Symlink(p, target); err != nil {
			l.WithField("target", target).WithField("error", err).Error("failed to create symlink")

			return sftp.ErrSshFxFailure
		}

		break
	case "Remove":
		if !fs.can(PermissionFileDelete) {
			return sftp.ErrSshFxPermissionDenied
		}

		if err := os.Remove(p); err != nil {
			if os.IsNotExist(err) {
				return sftp.ErrSSHFxNoSuchFile
			}

			l.WithField("error", err).Error("failed to remove a file")

			return sftp.ErrSshFxFailure
		}

		return sftp.ErrSshFxOk
	default:
		return sftp.ErrSshFxOpUnsupported
	}

	var fileLocation = p
	if target != "" {
		fileLocation = target
	}

	// Not failing here is intentional. We still made the file, it is just owned incorrectly
	// and will likely cause some issues. There is no logical check for if the file was removed
	// because both of those cases (Rmdir, Remove) have an explicit return rather than break.
	if err := os.Chown(fileLocation, fs.User.Uid, fs.User.Gid); err != nil {
		l.WithField("error", err).Warn("error chowning file")
	}

	return sftp.ErrSshFxOk
}

// Filelist is the handler for SFTP filesystem list calls. This will handle calls to list the contents of
// a directory as well as perform file/folder stat calls.
func (fs FileSystem) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	p, err := fs.buildPath(request.Filepath)
	if err != nil {
		return nil, sftp.ErrSshFxNoSuchFile
	}

	switch request.Method {
	case "List":
		if !fs.can(PermissionFileRead) {
			return nil, sftp.ErrSshFxPermissionDenied
		}

		files, err := ioutil.ReadDir(p)
		if err != nil {
			fs.logger.WithField("error", err).Error("error while listing directory")

			return nil, sftp.ErrSshFxFailure
		}

		return ListerAt(files), nil
	case "Stat":
		if !fs.can(PermissionFileRead) {
			return nil, sftp.ErrSshFxPermissionDenied
		}

		s, err := os.Stat(p)
		if os.IsNotExist(err) {
			return nil, sftp.ErrSshFxNoSuchFile
		} else if err != nil {
			fs.logger.WithField("source", p).WithField("error", err).Error("error performing stat on file")

			return nil, sftp.ErrSshFxFailure
		}

		return ListerAt([]os.FileInfo{s}), nil
	default:
		// Before adding readlink support we need to evaluate any potential security risks
		// as a result of navigating around to a location that is outside the home directory
		// for the logged in user. I don't foresee it being much of a problem, but I do want to
		// check it out before slapping some code here. Until then, we'll just return an
		// unsupported response code.
		return nil, sftp.ErrSshFxOpUnsupported
	}
}

// Determines if a user has permission to perform a specific action on the SFTP server. These
// permissions are defined and returned by the Panel API.
func (fs FileSystem) can(permission string) bool {
	// Server owners and super admins have their permissions returned as '[*]' via the Panel
	// API, so for the sake of speed do an initial check for that before iterating over the
	// entire array of permissions.
	if len(fs.Permissions) == 1 && fs.Permissions[0] == "*" {
		return true
	}

	// Not the owner or an admin, loop over the permissions that were returned to determine
	// if they have the passed permission.
	for _, p := range fs.Permissions {
		if p == permission {
			return true
		}
	}

	return false
}
