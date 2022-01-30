package remote

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/apex/log"
	"github.com/goccy/go-json"

	"github.com/pterodactyl/wings/parser"
)

// A generic type allowing for easy binding use when making requests to API
// endpoints that only expect a singular argument or something that would not
// benefit from being a typed struct.
//
// Inspired by gin.H, same concept.
type d map[string]interface{}

// Same concept as d, but a map of strings, used for querying GET requests.
type q map[string]string

type ClientOption func(c *client)

type Pagination struct {
	CurrentPage uint `json:"current_page"`
	From        uint `json:"from"`
	LastPage    uint `json:"last_page"`
	PerPage     uint `json:"per_page"`
	To          uint `json:"to"`
	Total       uint `json:"total"`
}

// ServerConfigurationResponse holds the server configuration data returned from
// the Panel. When a server process is started, Wings communicates with the
// Panel to fetch the latest build information as well as get all the details
// needed to parse the given Egg.
//
// This means we do not need to hit Wings each time part of the server is
// updated, and the Panel serves as the source of truth at all times. This also
// means if a configuration is accidentally wiped on Wings we can self-recover
// without too much hassle, so long as Wings is aware of what servers should
// exist on it.
type ServerConfigurationResponse struct {
	Settings             json.RawMessage       `json:"settings"`
	ProcessConfiguration *ProcessConfiguration `json:"process_configuration"`
}

// InstallationScript defines installation script information for a server
// process. This is used when a server is installed for the first time, and when
// a server is marked for re-installation.
type InstallationScript struct {
	ContainerImage string `json:"container_image"`
	Entrypoint     string `json:"entrypoint"`
	Script         string `json:"script"`
}

// RawServerData is a raw response from the API for a server.
type RawServerData struct {
	Uuid                 string          `json:"uuid"`
	Settings             json.RawMessage `json:"settings"`
	ProcessConfiguration json.RawMessage `json:"process_configuration"`
}

// SftpAuthRequest defines the request details that are passed along to the Panel
// when determining if the credentials provided to Wings are valid.
type SftpAuthRequest struct {
	User          string `json:"username"`
	Pass          string `json:"password"`
	IP            string `json:"ip"`
	SessionID     []byte `json:"session_id"`
	ClientVersion []byte `json:"client_version"`
}

// SftpAuthResponse is returned by the Panel when a pair of SFTP credentials
// is successfully validated. This will include the specific server that was
// matched as well as the permissions that are assigned to the authenticated
// user for the SFTP subsystem.
type SftpAuthResponse struct {
	Server      string   `json:"server"`
	Token       string   `json:"token"`
	Permissions []string `json:"permissions"`
}

type OutputLineMatcher struct {
	// The raw string to match against. This may or may not be prefixed with
	// regex: which indicates we want to match against the regex expression.
	raw []byte
	reg *regexp.Regexp
}

// Matches determines if the provided byte string matches the given regex or
// raw string provided to the matcher.
func (olm *OutputLineMatcher) Matches(s []byte) bool {
	if olm.reg == nil {
		return bytes.Contains(s, olm.raw)
	}
	return olm.reg.Match(s)
}

// String returns the matcher's raw comparison string.
func (olm *OutputLineMatcher) String() string {
	return string(olm.raw)
}

// UnmarshalJSON unmarshals the startup lines into individual structs for easier
// matching abilities.
func (olm *OutputLineMatcher) UnmarshalJSON(data []byte) error {
	var r string
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}

	olm.raw = []byte(r)
	if bytes.HasPrefix(olm.raw, []byte("regex:")) && len(olm.raw) > 6 {
		r, err := regexp.Compile(strings.TrimPrefix(string(olm.raw), "regex:"))
		if err != nil {
			log.WithField("error", err).WithField("raw", string(olm.raw)).Warn("failed to compile output line marked as being regex")
		}
		olm.reg = r
	}

	return nil
}

// ProcessStopConfiguration defines what is used when stopping an instance.
type ProcessStopConfiguration struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// ProcessConfiguration defines the process configuration for a given server
// instance. This sets what Wings is looking for to mark a server as done starting
// what to do when stopping, and what changes to make to the configuration file
// for a server.
type ProcessConfiguration struct {
	Startup struct {
		Done            []*OutputLineMatcher `json:"done"`
		UserInteraction []string             `json:"user_interaction"`
		StripAnsi       bool                 `json:"strip_ansi"`
	} `json:"startup"`
	Stop               ProcessStopConfiguration   `json:"stop"`
	ConfigurationFiles []parser.ConfigurationFile `json:"configs"`
}

type BackupRemoteUploadResponse struct {
	Parts    []string `json:"parts"`
	PartSize int64    `json:"part_size"`
}

type BackupRequest struct {
	Checksum     string `json:"checksum"`
	ChecksumType string `json:"checksum_type"`
	Size         int64  `json:"size"`
	Successful   bool   `json:"successful"`
}
