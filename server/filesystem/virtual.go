package filesystem

import (
	"context"
	"emperror.dev/errors"
	"github.com/pterodactyl/wings/internal/vhd"
)

// IsVirtual returns true if the filesystem is currently using a virtual disk.
func (fs *Filesystem) IsVirtual() bool {
	return fs.vhd != nil
}

// MountDisk will attempt to mount the underlying virtual disk for the server.
// If the disk is already mounted this is a no-op function. If the filesystem is
// not configured for virtual disks this function will panic.
func (fs *Filesystem) MountDisk(ctx context.Context) error {
	if !fs.IsVirtual() {
		panic(errors.New("filesystem: cannot call MountDisk on Filesystem instance without VHD present"))
	}
	err := fs.vhd.Mount(ctx)
	if errors.Is(err, vhd.ErrFilesystemMounted) {
		return nil
	}
	return errors.WrapIf(err, "filesystem: failed to mount VHD")
}
