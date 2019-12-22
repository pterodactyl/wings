package config

import (
	"fmt"
	"github.com/creasty/defaults"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type Configuration struct {
	// Determines if wings should be running in debug mode. This value is ignored
	// if the debug flag is passed through the command line arguments.
	Debug bool

	Api    ApiConfiguration
	System SystemConfiguration
	Docker DockerConfiguration

	// The amount of time in seconds that should elapse between disk usage checks
	// run by the daemon. Setting a higher number can result in better IO performance
	// at an increased risk of a malicious user creating a process that goes over
	// the assigned disk limits.
	DiskCheckTimeout int `yaml:"disk_check_timeout"`

	// Defines internal throttling configurations for server processes to prevent
	// someone from running an endless loop that spams data to logs.
	Throttles struct {
		// The number of data overage warnings (inclusive) that can accumulate
		// before a process is terminated.
		KillAtCount int `default:"5" yaml:"kill_at_count"`

		// The number of seconds that must elapse before the internal counter
		// begins decrementing warnings assigned to a process that is outputting
		// too much data.
		DecaySeconds int `default:"10" yaml:"decay"`

		// The total number of bytes allowed to be output by a server process
		// per interval.
		BytesPerInterval int `default:"4096" yaml:"bytes"`

		// The amount of time that should lapse between data output throttle
		// checks. This should be defined in milliseconds.
		CheckInterval int `defauly:"100" yaml:"check_interval"`
	}

	// The location where the panel is running that this daemon should connect to
	// to collect data and send events.
	PanelLocation string `yaml:"remote"`

	// The token used when performing operations. Requests to this instance must
	// validate aganist it.
	AuthenticationToken string `yaml:"token"`
}

// Defines basic system configuration settings.
type SystemConfiguration struct {
	// Directory where the server data is stored at.
	Data string `default:"/srv/daemon-data" yaml:"data"`

	// The user that should own all of the server files, and be used for containers.
	Username string `default:"pterodactyl" yaml:"username"`

	// Definitions for the user that gets created to ensure that we can quickly access
	// this information without constantly having to do a system lookup.
	User struct {
		Uid int
		Gid int
	}

	// Determines wether or not server data should be synced when the Daemon is started.
	// If set to false, data will only be synced when a server process is started, or
	// detected as started when booting.
	SyncServersOnBoot bool `default:"true" yaml:"sync_servers_on_boot"`

	// The path to the system's timezone file that will be mounted into running Docker containers.
	TimezonePath string `yaml:"timezone_path"`

	// Determines if permissions for a server should be set automatically on
	// daemon boot. This can take a long time on systems with many servers, or on
	// systems with servers containing thousands of files.
	//
	// Setting this to true by default helps us avoid a lot of support requests
	// from people that keep trying to move files around as a root user leading
	// to server permission issues.
	//
	// In production and heavy use environments where boot speed is essential,
	// this should be set to false as servers will self-correct permissions on
	// boot anyways.
	SetPermissionsOnBoot bool `default:"true" yaml:"set_permissions_on_boot"`

	// Determines if Wings should detect a server that stops with a normal exit code of
	// "0" as being crashed if the process stopped without any Wings interaction. E.g.
	// the user did not press the stop button, but the process stopped cleanly.
	DetectCleanExitAsCrash bool `default:"true" yaml:"detect_clean_exit_as_crash"`

	Sftp *SftpConfiguration `yaml:"sftp"`
}

// Defines the configuration of the internal SFTP server.
type SftpConfiguration struct {
	// If set to false, the internal SFTP server will not be booted and you will need
	// to run the SFTP server independent of this program.
	UseInternalSystem bool `default:"true" yaml:"use_internal"`
	// If set to true disk checking will not be performed. This will prevent the SFTP
	// server from checking the total size of a directory when uploading files.
	DisableDiskChecking bool `default:"false" yaml:"disable_disk_checking"`
	// The bind address of the SFTP server.
	Address string `default:"0.0.0.0" yaml:"bind_address"`
	// The bind port of the SFTP server.
	Port int `default:"2022" yaml:"bind_port"`
	// If set to true, no write actions will be allowed on the SFTP server.
	ReadOnly bool `default:"false" yaml:"read_only"`
}

type dockerNetworkInterfaces struct {
	V4 struct {
		Subnet  string `default:"172.18.0.0/16"`
		Gateway string `default:"172.18.0.1"`
	}

	V6 struct {
		Subnet  string `default:"fdba:17c8:6c94::/64"`
		Gateway string `default:"fdba:17c8:6c94::1011"`
	}
}

type DockerNetworkConfiguration struct {
	// The interface that should be used to create the network. Must not conflict
	// with any other interfaces in use by Docker or on the system.
	Interface string `default:"172.18.0.1"`

	// The name of the network to use. If this network already exists it will not
	// be created. If it is not found, a new network will be created using the interface
	// defined.
	Name       string                  `default:"pterodactyl_nw"`
	ISPN       bool                    `default:"false" yaml:"ispn"`
	Driver     string                  `default:"bridge"`
	IsInternal bool                    `default:"false" yaml:"is_internal"`
	EnableICC  bool                    `default:"true" yaml:"enable_icc"`
	Interfaces dockerNetworkInterfaces `yaml:"interfaces"`
}

// Defines the docker configuration used by the daemon when interacting with
// containers and networks on the system.
type DockerConfiguration struct {
	// Network configuration that should be used when creating a new network
	// for containers run through the daemon.
	Network DockerNetworkConfiguration `yaml:"network"`

	// If true, container images will be updated when a server starts if there
	// is an update available. If false the daemon will not attempt updates and will
	// defer to the host system to manage image updates.
	UpdateImages bool `default:"true" yaml:"update_images"`

	// The location of the Docker socket.
	Socket string `default:"/var/run/docker.sock"`

	// Defines the location of the timezone file on the host system that should
	// be mounted into the created containers so that they all use the same time.
	TimezonePath string `yaml:"timezone_path"`
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
		Enabled         bool   `default:"false"`
		CertificateFile string `yaml:"cert"`
		KeyFile         string `yaml:"key"`
	}

	// The maximum size for files uploaded through the Panel in bytes.
	UploadLimit int `default:"100" yaml:"upload_limit"`
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
	// in the structs. Valkues set in the configuration file take priority over the
	// default values.
	if err := defaults.Set(c); err != nil {
		return nil, err
	}

	// Replace environment variables within the configuration file with their
	// values from the host system.
	b = []byte(os.ExpandEnv(string(b)))

	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, err
	}

	return c, nil
}

var _config *Configuration
var _debugViaFlag bool

// Set the global configuration instance.
func Set(c *Configuration) {
	_config = c
}

func SetDebugViaFlag(d bool) {
	_debugViaFlag = d
}

// Get the global configuration instance.
func Get() *Configuration {
	return _config
}

// Ensures that the Pterodactyl core user exists on the system. This user will be the
// owner of all data in the root data directory and is used as the user within containers.
//
// If files are not owned by this user there will be issues with permissions on Docker
// mount points.
func (c *Configuration) EnsurePterodactylUser() (*user.User, error) {
	u, err := user.Lookup(c.System.Username)

	// If an error is returned but it isn't the unknown user error just abort
	// the process entirely. If we did find a user, return it immediately.
	if err == nil {
		return u, c.setSystemUser(u)
	} else if _, ok := err.(user.UnknownUserError); !ok {
		return nil, err
	}

	sysName, err := getSystemName()
	if err != nil {
		return nil, err
	}

	var command = fmt.Sprintf("useradd --system --no-create-home --shell /bin/false %s", c.System.Username)

	// Alpine Linux is the only OS we currently support that doesn't work with the useradd command, so
	// in those cases we just modify the command a bit to work as expected.
	if strings.HasPrefix(sysName, "Alpine") {
		command = fmt.Sprintf("adduser -S -D -H -G %[1]s -s /bin/false %[1]s", c.System.Username)

		// We have to create the group first on Alpine, so do that here before continuing on
		// to the user creation process.
		if _, err := exec.Command("addgroup", "-s", c.System.Username).Output(); err != nil {
			return nil, err
		}
	}

	split := strings.Split(command, " ")
	if _, err := exec.Command(split[0], split[1:]...).Output(); err != nil {
		return nil, err
	}

	if u, err := user.Lookup(c.System.Username); err != nil {
		return nil, err
	} else {
		return u, c.setSystemUser(u)
	}
}

// Set the system user into the configuration and then write it to the disk so that
// it is persisted on boot.
func (c *Configuration) setSystemUser(u *user.User) error {
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)

	c.System.Username = u.Username
	c.System.User.Uid = uid
	c.System.User.Gid = gid

	return c.WriteToDisk()
}

// Ensures that the configured data directory has the correct permissions assigned to
// all of the files and folders within.
func (c *Configuration) EnsureFilePermissions() error {
	// Don't run this unless it is configured to be run. On large system this can often slow
	// things down dramatically during the boot process.
	if !c.System.SetPermissionsOnBoot {
		return nil
	}

	r := regexp.MustCompile("^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$")

	files, err := ioutil.ReadDir(c.System.Data)
	if err != nil {
		return err
	}

	su, err := user.Lookup(c.System.Username)
	if err != nil {
		return err
	}

	wg := new(sync.WaitGroup)

	for _, file := range files {
		wg.Add(1)

		// Asynchronously run through the list of files and folders in the data directory. If
		// the item is not a folder, or is not a folder that matches the expected UUIDv4 format
		// skip over it.
		//
		// If we do have a positive match, run a chown aganist the directory.
		go func(f os.FileInfo) {
			defer wg.Done()

			if !f.IsDir() || !r.MatchString(f.Name()) {
				return
			}

			uid, _ := strconv.Atoi(su.Uid)
			gid, _ := strconv.Atoi(su.Gid)

			if err := os.Chown(path.Join(c.System.Data, f.Name()), uid, gid); err != nil {
				zap.S().Warnw("failed to chown server directory", zap.String("directory", f.Name()), zap.Error(err))
			}
		}(file)
	}

	wg.Wait()

	return nil
}

// Writes the configuration to the disk as a blocking operation by obtaining an exclusive
// lock on the file. This prevents something else from writing at the exact same time and
// leading to bad data conditions.
func (c *Configuration) WriteToDisk() error {
	f, err := os.OpenFile("config.yml", os.O_WRONLY, os.ModeExclusive)
	if err != nil {
		return err
	}
	defer f.Close()

	ccopy := *c
	// If debugging is set with the flag, don't save that to the configuration file, otherwise
	// you'll always end up in debug mode.
	if _debugViaFlag {
		ccopy.Debug = false
	}

	b, err := yaml.Marshal(&ccopy)
	if err != nil {
		return err
	}

	if _, err := f.Write(b); err != nil {
		return err
	}

	return nil
}

// Gets the system release name.
func getSystemName() (string, error) {
	cmd := exec.Command("lsb_release", "-is")

	b, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return string(b), nil
}
