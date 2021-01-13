package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/cobaugh/osrelease"
	"github.com/creasty/defaults"
	"github.com/gbrlsnchs/jwt/v3"
	"github.com/pterodactyl/wings/system"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

const DefaultLocation = "/etc/pterodactyl/config.yml"

type Configuration struct {
	sync.RWMutex `json:"-" yaml:"-"`

	// The location from which this configuration instance was instantiated.
	path string

	// Locker specific to writing the configuration to the disk, this happens
	// in areas that might already be locked so we don't want to crash the process.
	writeLock sync.Mutex

	// Determines if wings should be running in debug mode. This value is ignored
	// if the debug flag is passed through the command line arguments.
	Debug bool

	// A unique identifier for this node in the Panel.
	Uuid string

	// An identifier for the token which must be included in any requests to the panel
	// so that the token can be looked up correctly.
	AuthenticationTokenId string `json:"token_id" yaml:"token_id"`

	// The token used when performing operations. Requests to this instance must
	// validate against it.
	AuthenticationToken string `json:"token" yaml:"token"`

	Api    ApiConfiguration    `json:"api" yaml:"api"`
	System SystemConfiguration `json:"system" yaml:"system"`
	Docker DockerConfiguration `json:"docker" yaml:"docker"`

	// Defines internal throttling configurations for server processes to prevent
	// someone from running an endless loop that spams data to logs.
	Throttles ConsoleThrottles

	// The location where the panel is running that this daemon should connect to
	// to collect data and send events.
	PanelLocation string                   `json:"remote" yaml:"remote"`
	RemoteQuery   RemoteQueryConfiguration `json:"remote_query" yaml:"remote_query"`

	// AllowedMounts is a list of allowed host-system mount points.
	// This is required to have the "Server Mounts" feature work properly.
	AllowedMounts []string `json:"-" yaml:"allowed_mounts"`

	// AllowedOrigins is a list of allowed request origins.
	// The Panel URL is automatically allowed, this is only needed for adding
	// additional origins.
	AllowedOrigins []string `json:"allowed_origins" yaml:"allowed_origins"`
}

// Defines the configuration of the internal SFTP server.
type SftpConfiguration struct {
	// The bind address of the SFTP server.
	Address string `default:"0.0.0.0" json:"bind_address" yaml:"bind_address"`
	// The bind port of the SFTP server.
	Port int `default:"2022" json:"bind_port" yaml:"bind_port"`
	// If set to true, no write actions will be allowed on the SFTP server.
	ReadOnly bool `default:"false" yaml:"read_only"`
}

// Defines the configuration for the internal API that is exposed by the
// daemon webserver.
type ApiConfiguration struct {
	// The interface that the internal webserver should bind to.
	Host string `default:"0.0.0.0" yaml:"host"`

	// The port that the internal webserver should bind to.
	Port int `default:"8080" yaml:"port"`

	// SSL configuration for the daemon.
	Ssl struct {
		Enabled         bool   `json:"enabled" yaml:"enabled"`
		CertificateFile string `json:"cert" yaml:"cert"`
		KeyFile         string `json:"key" yaml:"key"`
	}

	// Determines if functionality for allowing remote download of files into server directories
	// is enabled on this instance. If set to "true" remote downloads will not be possible for
	// servers.
	DisableRemoteDownload bool `json:"disable_remote_download" yaml:"disable_remote_download"`

	// The maximum size for files uploaded through the Panel in bytes.
	UploadLimit int `default:"100" json:"upload_limit" yaml:"upload_limit"`
}

// Defines the configuration settings for remote requests from Wings to the Panel.
type RemoteQueryConfiguration struct {
	// The amount of time in seconds that Wings should allow for a request to the Panel API
	// to complete. If this time passes the request will be marked as failed. If your requests
	// are taking longer than 30 seconds to complete it is likely a performance issue that
	// should be resolved on the Panel, and not something that should be resolved by upping this
	// number.
	Timeout uint `default:"30" yaml:"timeout"`

	// The number of servers to load in a single request to the Panel API when booting the
	// Wings instance. A single request is initially made to the Panel to get this number
	// of servers, and then the pagination status is checked and additional requests are
	// fired off in parallel to request the remaining pages.
	//
	// It is not recommended to change this from the default as you will likely encounter
	// memory limits on your Panel instance. In the grand scheme of things 4 requests for
	// 50 servers is likely just as quick as two for 100 or one for 400, and will certainly
	// be less likely to cause performance issues on the Panel.
	BootServersPerPage uint `default:"50" yaml:"boot_servers_per_page"`
}

// Reads the configuration from the provided file and returns the configuration
// object that can then be used.
func ReadConfiguration(path string) (*Configuration, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	c := new(Configuration)
	// Configures the default values for many of the configuration options present
	// in the structs. Values set in the configuration file take priority over the
	// default values.
	if err := defaults.Set(c); err != nil {
		return nil, err
	}

	// Track the location where we created this configuration.
	c.unsafeSetPath(path)

	// Replace environment variables within the configuration file with their
	// values from the host system.
	b = []byte(os.ExpandEnv(string(b)))

	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, err
	}

	return c, nil
}

var mu sync.RWMutex

var _config *Configuration
var _jwtAlgo *jwt.HMACSHA
var _debugViaFlag bool

// Set the global configuration instance. This is a blocking operation such that
// anything trying to set a different configuration value, or read the configuration
// will be paused until it is complete.
func Set(c *Configuration) {
	mu.Lock()

	if _config == nil || _config.AuthenticationToken != c.AuthenticationToken {
		_jwtAlgo = jwt.NewHS256([]byte(c.AuthenticationToken))
	}

	_config = c
	mu.Unlock()
}

func SetDebugViaFlag(d bool) {
	_debugViaFlag = d
}

// Get the global configuration instance. This is a read-safe operation that will block
// if the configuration is presently being modified.
func Get() *Configuration {
	mu.RLock()
	defer mu.RUnlock()

	return _config
}

// Returns the in-memory JWT algorithm.
func GetJwtAlgorithm() *jwt.HMACSHA {
	mu.RLock()
	defer mu.RUnlock()

	return _jwtAlgo
}

// Create a new struct and set the path where it should be stored.
func NewFromPath(path string) (*Configuration, error) {
	c := new(Configuration)
	if err := defaults.Set(c); err != nil {
		return c, err
	}

	c.unsafeSetPath(path)

	return c, nil
}

// Sets the path where the configuration file is located on the server. This function should
// not be called except by processes that are generating the configuration such as the configuration
// command shipped with this software.
func (c *Configuration) unsafeSetPath(path string) {
	c.Lock()
	c.path = path
	c.Unlock()
}

// Returns the path for this configuration file.
func (c *Configuration) GetPath() string {
	c.RLock()
	defer c.RUnlock()

	return c.path
}

// EnsurePterodactylUser ensures that the Pterodactyl core user exists on the
// system. This user will be the owner of all data in the root data directory
// and is used as the user within containers.
//
// If files are not owned by this user there will be issues with permissions on
// Docker mount points.
func EnsurePterodactylUser() error {
	sysName, err := getSystemName()
	if err != nil {
		return err
	}

	// Our way of detecting if wings is running inside of Docker.
	if sysName == "busybox" {
		viper.Set("system.username", system.FirstNotEmpty(os.Getenv("WINGS_USERNAME"), "pterodactyl"))
		viper.Set("system.user.uid", system.MustInt(system.FirstNotEmpty(os.Getenv("WINGS_UID"), "988")))
		viper.Set("system.user.gid", system.MustInt(system.FirstNotEmpty(os.Getenv("WINGS_GID"), "988")))
		return nil
	}

	username := viper.GetString("system.username")
	u, err := user.Lookup(username)
	// If an error is returned but it isn't the unknown user error just abort
	// the process entirely. If we did find a user, return it immediately.
	if err != nil {
		if _, ok := err.(user.UnknownUserError); !ok {
			return err
		}
	} else {
		viper.Set("system.user.uid", system.MustInt(u.Uid))
		viper.Set("system.user.gid", system.MustInt(u.Gid))
		return nil
	}

	command := fmt.Sprintf("useradd --system --no-create-home --shell /usr/sbin/nologin %s", username)
	// Alpine Linux is the only OS we currently support that doesn't work with the useradd
	// command, so in those cases we just modify the command a bit to work as expected.
	if strings.HasPrefix(sysName, "alpine") {
		command = fmt.Sprintf("adduser -S -D -H -G %[1]s -s /sbin/nologin %[1]s", username)
		// We have to create the group first on Alpine, so do that here before continuing on
		// to the user creation process.
		if _, err := exec.Command("addgroup", "-S", username).Output(); err != nil {
			return err
		}
	}

	split := strings.Split(command, " ")
	if _, err := exec.Command(split[0], split[1:]...).Output(); err != nil {
		return err
	}

	u, err = user.Lookup(username)
	if err != nil {
		return err
	}
	viper.Set("system.user.uid", system.MustInt(u.Uid))
	viper.Set("system.user.gid", system.MustInt(u.Gid))
	return nil
}

// Writes the configuration to the disk as a blocking operation by obtaining an exclusive
// lock on the file. This prevents something else from writing at the exact same time and
// leading to bad data conditions.
func (c *Configuration) WriteToDisk() error {
	// Obtain an exclusive write against the configuration file.
	c.writeLock.Lock()
	defer c.writeLock.Unlock()

	ccopy := *c
	// If debugging is set with the flag, don't save that to the configuration file, otherwise
	// you'll always end up in debug mode.
	if _debugViaFlag {
		ccopy.Debug = false
	}

	if c.path == "" {
		return errors.New("cannot write configuration, no path defined in struct")
	}

	b, err := yaml.Marshal(&ccopy)
	if err != nil {
		return err
	}

	if err := ioutil.WriteFile(c.GetPath(), b, 0644); err != nil {
		return err
	}

	return nil
}

// Gets the system release name.
func getSystemName() (string, error) {
	// use osrelease to get release version and ID
	if release, err := osrelease.Read(); err != nil {
		return "", err
	} else {
		return release["ID"], nil
	}
}
