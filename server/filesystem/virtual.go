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

// ConfigureDisk will attempt to create a new VHD if there is not one already
// created for the filesystem. If there is this method will attempt to resize
// the underlying data volume. Passing a size of 0 or less will panic.
func (fs *Filesystem) ConfigureDisk(ctx context.Context, size int64) error {
	if size <= 0 {
		panic("filesystem: attempt to configure disk with empty size")
	}
	if fs.vhd == nil {
		fs.vhd = vhd.New(size, vhd.DiskPath(fs.uuid), fs.root)
		if err := fs.MountDisk(ctx); err != nil {
			return errors.WithStackIf(err)
		}
	}
	// Resize the disk now that it is for sure mounted and exists on the system.
	if err := fs.vhd.Resize(ctx, size); err != nil {
		return errors.WithStackIf(err)
	}
	return nil
}

// MountDisk will attempt to mount the underlying virtual disk for the server.
// If the disk is already mounted this is a no-op function.
func (fs *Filesystem) MountDisk(ctx context.Context) error {
	err := fs.vhd.Mount(ctx)
	if errors.Is(err, vhd.ErrFilesystemMounted) {
		return nil
	}
	return errors.WrapIf(err, "filesystem: failed to mount VHD")
}
