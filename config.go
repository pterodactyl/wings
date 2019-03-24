package main

import (
    "gopkg.in/yaml.v2"
    "io/ioutil"
    "os"
)

type Configuration struct {
    // Determines if wings should be running in debug mode. This value is ignored
    // if the debug flag is passed through the command line arguments.
    Debug bool

    // Directory where the server data is stored at.
    Data string

    Api    *ApiConfiguration
    Docker *DockerConfiguration

    // Determines if permissions for a server should be set automatically on
    // daemon boot. This can take a long time on systems with many servers, or on
    // systems with servers containing thousands of files.
    SetPermissionsOnBoot bool `yaml:"set_permissions_on_boot"`

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
        KillAtCount int `yaml:"kill_at_count"`

        // The number of seconds that must elapse before the internal counter
        // begins decrementing warnings assigned to a process that is outputting
        // too much data.
        DecaySeconds int `yaml:"decay"`

        // The total number of bytes allowed to be output by a server process
        // per interval.
        BytesPerInterval int `yaml:"bytes"`

        // The amount of time that should lapse between data output throttle
        // checks. This should be defined in milliseconds.
        CheckInterval int `yaml:"check_interval"`
    }

    // The location where the panel is running that this daemon should connect to
    // to collect data and send events.
    PanelLocation string `yaml:"remote"`

    // The token used when performing operations. Requests to this instance must
    // validate aganist it.
    AuthenticationToken string `yaml:"token"`
}

// Defines the configuration for the internal API that is exposed by the
// daemon webserver.
type ApiConfiguration struct {
    // The interface that the internal webserver should bind to.
    Host string

    // The port that the internal webserver should bind to.
    Port int

    // SSL configuration for the daemon.
    Ssl struct {
        Enabled         bool
        CertificateFile string `yaml:"cert"`
        KeyFile         string `yaml:"key"`
    }

    // The maximum size for files uploaded through the Panel in bytes.
    UploadLimit int `yaml:"upload_limit"`
}

// Defines the docker configuration used by the daemon when interacting with
// containers and networks on the system.
type DockerConfiguration struct {
    Container struct {
        User string
    }

    // Network configuration that should be used when creating a new network
    // for containers run through the daemon.
    Network struct {
        // The interface that should be used to create the network. Must not conflict
        // with any other interfaces in use by Docker or on the system.
        Interface string

        // The name of the network to use. If this network already exists it will not
        // be created. If it is not found, a new network will be created using the interface
        // defined.
        Name string
    }

    // If true, container images will be updated when a server starts if there
    // is an update available. If false the daemon will not attempt updates and will
    // defer to the host system to manage image updates.
    UpdateImages bool `yaml:"update_images"`

    // The location of the Docker socket.
    Socket string

    // Defines the location of the timezone file on the host system that should
    // be mounted into the created containers so that they all use the same time.
    TimezonePath string `yaml:"timezone_path"`
}

// Configures the default values for many of the configuration options present
// in the structs. If these values are set in the configuration file they will
// be overridden. However, if they don't exist and we don't set these here, all
// of the places in the code using them will need to be doing checks, which is
// a tedious thing to have to do.
func (c *Configuration) SetDefaults() {
    // Set the default data directory.
    c.Data = "/srv/daemon-data"

    // By default the internal webserver should bind to all interfaces and
    // be served on port 8080.
    c.Api = &ApiConfiguration{
        Host: "0.0.0.0",
        Port: 8080,
    }

    // Setting this to true by default helps us avoid a lot of support requests
    // from people that keep trying to move files around as a root user leading
    // to server permission issues.
    //
    // In production and heavy use environments where boot speed is essential,
    // this should be set to false as servers will self-correct permissions on
    // boot anyways.
    c.SetPermissionsOnBoot = true

    // Configure the default throttler implementation. This should work fine
    // for 99% of people running servers on the platform. The occasional host
    // might need to tweak them to be less restrictive depending on their hardware
    // and target audience.
    c.Throttles.KillAtCount = 5
    c.Throttles.DecaySeconds = 10
    c.Throttles.BytesPerInterval = 4096
    c.Throttles.CheckInterval = 100

    // Configure the defaults for Docker connection and networks.
    c.Docker = &DockerConfiguration{}
    c.Docker.UpdateImages = true
    c.Docker.Socket = "/var/run/docker.sock"
    c.Docker.Network.Name = "pterodactyl_nw"
    c.Docker.Network.Interface = "172.18.0.1"
}

// Reads the configuration from the provided file and returns the configuration
// object that can then be used.
func ReadConfiguration(path string) (*Configuration, error) {
    b, err := ioutil.ReadFile(path)
    if err != nil {
        return nil, err
    }

    c := &Configuration{}
    c.SetDefaults()

    // Replace environment variables within the configuration file with their
    // values from the host system.
    b = []byte(os.ExpandEnv(string(b)))

    if err := yaml.Unmarshal(b, c); err != nil {
        return nil, err
    }

    return c, nil
}
