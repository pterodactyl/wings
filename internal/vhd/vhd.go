package vhd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"

	"emperror.dev/errors"
	"github.com/pterodactyl/wings/config"
	"github.com/spf13/afero"
)

var (
	ErrInvalidDiskPathTarget = errors.Sentinel("vhd: disk path is a directory or symlink")
	ErrMountPathNotDirectory = errors.Sentinel("vhd: mount point is not a directory")
	ErrFilesystemMounted     = errors.Sentinel("vhd: filesystem is already mounted")
	ErrFilesystemExists      = errors.Sentinel("vhd: filesystem already exists on disk")
)

// hasExitCode allows this code to test the response error to see if there is
// an exit code available from the command call that can be used to determine if
// something went wrong.
type hasExitCode interface {
	ExitCode() int
}

// Commander defines an interface that must be met for executing commands on the
// underlying OS. By default the vhd package will use Go's exec.Cmd type for
// execution. This interface allows stubbing out on tests, or potentially custom
// setups down the line.
type Commander interface {
	Run() error
	Output() ([]byte, error)
	String() string
}

// CommanderProvider is a function that provides a struct meeting the Commander
// interface requirements.
type CommanderProvider func(ctx context.Context, name string, args ...string) Commander

// CfgOption is a configuration option callback for the Disk.
type CfgOption func(d *Disk) *Disk

// Disk represents the underlying virtual disk for the instance.
type Disk struct {
	// The total size of the disk allowed in bytes.
	size int64
	// The path where the disk image should be created.
	diskPath string
	// The point at which this disk should be made available on the system. This
	// is where files can be read/written to.
	mountAt   string
	fs        afero.Fs
	commander CommanderProvider
}

// New returns a new Disk instance. The "size" parameter should be provided in
// bytes of space allowed for the disk. An additional slice of option callbacks
// can be provided to programatically swap out the underlying filesystem
// implementation or the underlying command exection engine.
func New(size int64, diskPath string, mountAt string, opts ...func(*Disk)) *Disk {
	if diskPath == "" || mountAt == "" {
		panic("vhd: cannot specify an empty disk or mount path")
	}
	d := Disk{
		size:     size,
		diskPath: diskPath,
		mountAt:  mountAt,
		fs:       afero.NewOsFs(),
		commander: func(ctx context.Context, name string, args ...string) Commander {
			return exec.CommandContext(ctx, name, args...)
		},
	}
	for _, opt := range opts {
		opt(&d)
	}
	return &d
}

// WithFs allows for a different underlying filesystem to be provided to the
// virtual disk manager.
func WithFs(fs afero.Fs) func(*Disk) {
	return func(d *Disk) {
		d.fs = fs
	}
}

// WithCommander allows a different Commander provider to be provided.
func WithCommander(c CommanderProvider) func(*Disk) {
	return func(d *Disk) {
		d.commander = c
	}
}

func (d *Disk) Path() string {
	return d.diskPath
}

func (d *Disk) MountPath() string {
	return d.mountAt
}

// Exists reports if the disk exists on the system yet or not. This only verifies
// the presence of the disk image, not the validity of it. An error is returned
// if the path exists but the destination is not a file or is a symlink.
func (d *Disk) Exists() (bool, error) {
	st, err := d.fs.Stat(d.diskPath)
	if err != nil && os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, errors.WithStack(err)
	}
	if !st.IsDir() && st.Mode()&os.ModeSymlink == 0 {
		return true, nil
	}
	return false, errors.WithStack(ErrInvalidDiskPathTarget)
}

// IsMounted checks to see if the given disk is currently mounted.
func (d *Disk) IsMounted(ctx context.Context) (bool, error) {
	find := d.mountAt + " ext4"
	cmd := d.commander(ctx, "grep", "-qs", find, "/proc/mounts")
	if err := cmd.Run(); err != nil {
		if v, ok := err.(hasExitCode); ok {
			if v.ExitCode() == 1 {
				return false, nil
			}
		}
		return false, errors.Wrap(err, "vhd: failed to execute grep for mount existence")
	}
	return true, nil
}

// Mount attempts to mount the disk as configured. If it does not exist or the
// mount command fails an error will be returned to the caller. This does not
// attempt to create the disk if it is missing from the filesystem.
//
// Attempting to mount a disk which does not exist will result in an error being
// returned to the caller. If the disk is already mounted an ErrFilesystemMounted
// error is returned to the caller.
func (d *Disk) Mount(ctx context.Context) error {
	if isMounted, err := d.IsMounted(ctx); err != nil {
		return errors.WithStackIf(err)
	} else if isMounted {
		return ErrFilesystemMounted
	}
	if st, err := d.fs.Stat(d.mountAt); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "vhd: failed to stat mount path")
	} else if os.IsNotExist(err) {
		if err := d.fs.MkdirAll(d.mountAt, 0600); err != nil {
			return errors.Wrap(err, "vhd: failed to create mount path")
		}
	} else if !st.IsDir() {
		return errors.WithStack(ErrMountPathNotDirectory)
	}
	u := config.Get().System.User
	if err := d.fs.Chown(d.mountAt, u.Uid, u.Gid); err != nil {
		return errors.Wrap(err, "vhd: failed to chown mount point")
	}
	cmd := d.commander(ctx, "mount", "-t", "auto", "-o", "loop", d.diskPath, d.mountAt)
	if _, err := cmd.Output(); err != nil {
		msg := "vhd: failed to mount disk"
		if v, ok := err.(*exec.ExitError); ok {
			msg = msg + ": " + strings.Trim(string(v.Stderr), ".\n")
		}
		return errors.Wrap(err, msg)
	}
	return nil
}

// Unmount attempts to unmount the disk from the system. If the disk is not
// currently mounted this function is a no-op and no error is returned. Any
// other error encountered while unmounting will return an error to the caller.
func (d *Disk) Unmount(ctx context.Context) error {
	cmd := d.commander(ctx, "umount", d.mountAt)
	if err := cmd.Run(); err != nil {
		if v, ok := err.(hasExitCode); !ok || v.ExitCode() != 32 {
			return errors.Wrap(err, "vhd: failed to execute unmount command for disk")
		}
	}
	return nil
}

// Allocate executes the "fallocate" command on the disk. This will first unmount
// the disk from the system before attempting to actually allocate the space. If
// this disk already exists on the machine it will be resized accordingly.
//
// DANGER! This will unmount the disk from the machine while performing this
// action, use caution when calling it during normal processes.
func (d *Disk) Allocate(ctx context.Context) error {
	if exists, err := d.Exists(); exists {
		// If the disk currently exists attempt to unmount the mount point before
		// allocating space.
		if err := d.Unmount(ctx); err != nil {
			return errors.WithStackIf(err)
		}
	} else if err != nil {
		return errors.Wrap(err, "vhd: failed to check for existence of root disk")
	}
	trim := path.Base(d.diskPath)
	if err := d.fs.MkdirAll(strings.TrimSuffix(d.diskPath, trim), 0600); err != nil {
		return errors.Wrap(err, "vhd: failed to create base vhd disk directory")
	}
	// We use 1024 as the multiplier for all of the disk space logic within the
	// application. Passing "K" (/1024) is the same as "KiB" for fallocate, but
	// is different than "KB" (/1000).
	cmd := d.commander(ctx, "fallocate", "-l", fmt.Sprintf("%dK", d.size/1024), d.diskPath)
	if _, err := cmd.Output(); err != nil {
		msg := "vhd: failed to execute fallocate command"
		if v, ok := err.(*exec.ExitError); ok {
			msg = msg + ": " + strings.Trim(string(v.Stderr), ".\n")
		}
		return errors.Wrap(err, msg)
	}
	return nil
}

// MakeFilesystem will attempt to execute the "mkfs" command against the disk on
// the machine. If the disk has already been created this command will return an
// ErrFilesystemExists error to the caller. You should manually unmount the disk
// if it shouldn't be mounted at this point.
func (d *Disk) MakeFilesystem(ctx context.Context) error {
	// If no error is returned when mounting DO NOT execute this command as it will
	// completely destroy the data stored at that location.
	err := d.Mount(ctx)
	if err == nil || errors.Is(err, ErrFilesystemMounted) {
		// If it wasn't already mounted try to clean up at this point and unmount
		// the disk. If this fails just ignore it for now.
		if err != nil {
			_ = d.Unmount(ctx)
		}
		return ErrFilesystemExists
	}
	if !strings.Contains(err.Error(), "can't find in /etc/fstab") && !strings.Contains(err.Error(), "exit status 32") {
		return errors.WrapIf(err, "vhd: unexpected error from mount command")
	}
	// As long as we got an error back that was because we couldn't find thedisk
	// in the /etc/fstab file we're good. Otherwise it means the disk probably exists
	// or something else went wrong.
	//
	// Because this is a destructive command and non-tty based exection of it implies
	// "-F" (force), we need to only run it when we can guarantee it doesn't already
	// exist. No vague "maybe that error is expected" allowed here.
	cmd := d.commander(ctx, "mkfs", "-t", "ext4", d.diskPath)
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "vhd: failed to make filesystem for disk")
	}
	return nil
}
