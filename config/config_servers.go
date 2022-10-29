package config

type FSDriver string

const (
	FSDriverLocal FSDriver = "local"
	FSDriverVHD   FSDriver = "vhd"
)

type Servers struct {
	// Filesystem defines all of the filesystem specific settings used for servers.
	Filesystem Filesystem `json:"filesystem" yaml:"filesystem"`
}

type Filesystem struct {
	// Driver defines the underlying filesystem driver that is used when a server is
	// created on the system. This currently supports either of the following drivers:
	//
	// local: the local driver is the default one used by Wings. This offloads all of the
	//        disk limit enforcement to Wings itself. This has a performance impact but is
	//        the most compatiable with all systems.
	//   vhd: the vhd driver uses "virtual" disks on the host system to enforce disk limits
	//        on the server. This is more performant since calculations do not need to be made
	//        by Wings itself when enforcing limits. It also avoids vulnerabilities that exist
	//        in the local driver which allow malicious processes to quickly create massive files
	//        before Wings is able to detect and stop them from being written.
	Driver FSDriver `default:"local" json:"driver" yaml:"driver"`
}
