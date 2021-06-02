package vhd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"emperror.dev/errors"
)

var (
	ErrFilesystemMounted = errors.Sentinel("vhd: filesystem is already mounted")
	ErrFilesystemExists  = errors.Sentinel("vhd: filesystem already exists on disk")
)

type Disk struct {
	size int64

	diskPath string
	mountAt  string
}

// New returns a new Disk instance. The "size" parameter should be provided in
// megabytes of space allowed for the disk.
func New(size int64, diskPath string, mountAt string) *Disk {
	if diskPath == "" || mountAt == "" {
		panic("vhd: cannot specify an empty disk or mount path")
	}
	return &Disk{size, diskPath, mountAt}
}

// Exists reports if the disk exists on the system yet or not. This only verifies
// the presence of the disk image, not the validity of it.
func (d *Disk) Exists() (bool, error) {
	_, err := os.Lstat(d.diskPath)
	if err == nil || os.IsNotExist(err) {
		return err == nil, nil
	}
	return false, errors.WithStack(err)
}

// IsMounted checks to see if the given disk is currently mounted.
func (d *Disk) IsMounted(ctx context.Context) (bool, error) {
	find := d.mountAt + " ext4"
	cmd := exec.CommandContext(ctx, "grep", "-qs", find, "/proc/mounts")
	if err := cmd.Run(); err != nil {
		if v, ok := err.(*exec.ExitError); ok {
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
	if _, err := os.Lstat(d.mountAt); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "vhd: failed to stat mount path")
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(d.mountAt, 0600); err != nil {
			return errors.Wrap(err, "vhd: failed to create mount path")
		}
	}
	if isMounted, err := d.IsMounted(ctx); err != nil {
		return errors.WithStackIf(err)
	} else if isMounted {
		return ErrFilesystemMounted
	}
	cmd := exec.CommandContext(ctx, "mount", "-t", "auto", "-o", "loop", d.diskPath, d.mountAt)
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
	cmd := exec.CommandContext(ctx, "umount", d.mountAt)
	if err := cmd.Run(); err != nil {
		if v, ok := err.(*exec.ExitError); !ok || v.ExitCode() != 32 {
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
	cmd := exec.CommandContext(ctx, "fallocate", "-l", fmt.Sprintf("%dM", d.size), d.diskPath)
	fmt.Println(cmd.String())
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
	if !strings.Contains(err.Error(), "can't find in /etc/fstab") {
		return errors.WrapIf(err, "vhd: unexpected error from mount command")
	}
	// As long as we got an error back that was because we couldn't find thedisk
	// in the /etc/fstab file we're good. Otherwise it means the disk probably exists
	// or something else went wrong.
	//
	// Because this is a destructive command and non-tty based exection of it implies
	// "-F" (force), we need to only run it when we can guarantee it doesn't already
	// exist. No vague "maybe that error is expected" allowed here.
	cmd := exec.CommandContext(ctx, "mkfs", "-t", "ext4", d.diskPath)
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "vhd: failed to make filesystem for disk")
	}
	return nil
}
