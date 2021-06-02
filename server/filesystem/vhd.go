package filesystem

import (
	"path/filepath"
	"strings"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/vhd"
)

func (fs *Filesystem) NewVHD() *vhd.Disk {
	parts := strings.Split(fs.root, "/")
	disk := filepath.Join(config.Get().System.Data, ".disks/", parts[len(parts)-1]+".img")

	return vhd.New(250, disk, fs.root)
	// return vhd.New(fs.diskLimit/1024/1024, disk, fs.root)
}
