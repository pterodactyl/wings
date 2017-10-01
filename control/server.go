package control

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/Pterodactyl/wings/config"
	"github.com/Pterodactyl/wings/constants"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// ErrServerExists is returned when a server already exists on creation.
type ErrServerExists struct {
	id string
}

func (e ErrServerExists) Error() string {
	return "server " + e.id + " already exists"
}

// Server is a Server
type Server interface {
	Start() error
	Stop() error
	Restart() error
	Kill() error
	Exec(command string) error
	Rebuild() error

	Save() error

	Environment() (Environment, error)

	HasPermission(string, string) bool
}

// ServerStruct is a single instance of a Service managed by the panel
type ServerStruct struct {
	// ID is the unique identifier of the server
	ID string `json:"uuid"`

	// ServiceName is the name of the service. It is mainly used to allow storing the service
	// in the config
	ServiceName string `json:"serviceName"`
	service     *Service
	environment Environment

	// StartupCommand is the command executed in the environment to start the server
	StartupCommand string `json:"startupCommand"`

	// DockerContainer holds information regarding the docker container when the server
	// is running in a docker environment
	DockerContainer dockerContainer `json:"dockerContainer"`

	// EnvironmentVariables are set in the Environment the server is running in
	EnvironmentVariables map[string]string `json:"env"`

	// Allocations contains the ports and ip addresses assigned to the server
	Allocations allocations `json:"allocation"`

	// Settings are the environment settings and limitations for the server
	Settings settings `json:"settings"`

	// Keys are some auth keys we will hopefully replace by something better.
	Keys map[string][]string `json:"keys"`
}

type allocations struct {
	Ports    []int16 `json:"ports"`
	MainIP   string  `json:"ip"`
	MainPort int16   `json:"port"`
}

type settings struct {
	Memory int64  `json:"memory"`
	Swap   int64  `json:"swap"`
	IO     int64  `json:"io"`
	CPU    int16  `json:"cpu"`
	Disk   int64  `json:"disk"`
	Image  string `json:"image"`
	User   string `json:"user"`
	UserID int16  `json:"userID"`
}

type dockerContainer struct {
	ID    string `json:"id"`
	Image string `json:"image"`
}

// ensure server implements Server
var _ Server = &ServerStruct{}

type serversMap map[string]*ServerStruct

var servers = make(serversMap)

// LoadServerConfigurations loads the configured servers from a specified path
func LoadServerConfigurations(path string) error {
	serverFiles, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}
	servers = make(serversMap)

	for _, file := range serverFiles {
		if file.IsDir() {
			server, err := loadServerConfiguration(filepath.Join(path, file.Name(), constants.ServerConfigFile))
			if err != nil {
				return err
			}
			servers[server.ID] = server
		}
	}

	return nil
}

func loadServerConfiguration(path string) (*ServerStruct, error) {
	file, err := ioutil.ReadFile(path)

	if err != nil {
		return nil, err
	}

	server := &ServerStruct{}
	if err := json.Unmarshal(file, server); err != nil {
		return nil, err
	}
	return server, nil
}

func storeServerConfiguration(server *ServerStruct) error {
	serverJSON, err := json.MarshalIndent(server, "", constants.JSONIndent)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(server.path(), constants.DefaultFolderPerms); err != nil {
		return err
	}
	if err := ioutil.WriteFile(server.configFilePath(), serverJSON, constants.DefaultFilePerms); err != nil {
		return err
	}
	return nil
}

func storeServerConfigurations() error {
	for _, s := range servers {
		if err := storeServerConfiguration(s); err != nil {
			return err
		}
	}
	return nil
}

func deleteServerFolder(id string) error {
	path := filepath.Join(viper.GetString(config.DataPath), constants.ServersPath, id)
	folder, err := os.Stat(path)
	if os.IsNotExist(err) || !folder.IsDir() {
		return err
	}
	return os.RemoveAll(path)
}

// GetServers returns an array of all servers the daemon manages
func GetServers() []Server {
	serverArray := make([]Server, len(servers))
	i := 0
	for _, s := range servers {
		serverArray[i] = s
		i++
	}
	return serverArray
}

// GetServer returns the server identified by the provided uuid
func GetServer(id string) Server {
	server := servers[id]
	if server == nil {
		return nil // https://golang.org/doc/faq#nil_error
	}
	return server
}

// CreateServer creates a new server
func CreateServer(server *ServerStruct) (Server, error) {
	if servers[server.ID] != nil {
		return nil, ErrServerExists{server.ID}
	}
	servers[server.ID] = server
	if err := server.Save(); err != nil {
		return nil, err
	}
	return server, nil
}

// DeleteServer deletes a server and all related files
// NOTE: This is not reversible.
func DeleteServer(id string) error {
	if err := deleteServerFolder(id); err != nil {
		log.WithField("server", id).WithError(err).Error("Failed to delete server.")
	}
	delete(servers, id)
	return nil
}

func (s *ServerStruct) Start() error {
	env, err := s.Environment()
	if err != nil {
		return err
	}
	if !env.Exists() {
		if err := env.Create(); err != nil {
			return err
		}
	}
	return env.Start()
}

func (s *ServerStruct) Stop() error {
	env, err := s.Environment()
	if err != nil {
		return err
	}
	return env.Stop()
}

func (s *ServerStruct) Restart() error {
	if err := s.Stop(); err != nil {
		return err
	}
	return s.Start()
}

func (s *ServerStruct) Kill() error {
	env, err := s.Environment()
	if err != nil {
		return err
	}
	return env.Kill()
}

func (s *ServerStruct) Exec(command string) error {
	env, err := s.Environment()
	if err != nil {
		return err
	}
	return env.Exec(command)
}

func (s *ServerStruct) Rebuild() error {
	env, err := s.Environment()
	if err != nil {
		return err
	}
	return env.ReCreate()
}

// Service returns the server's service configuration
func (s *ServerStruct) Service() *Service {
	if s.service == nil {
		// TODO: Properly use the correct service, mock for now.
		s.service = &Service{
			DockerImage:     "quay.io/pterodactyl/core:java",
			EnvironmentName: "docker",
		}
	}
	return s.service
}

// UUIDShort returns the first block of the UUID
func (s *ServerStruct) UUIDShort() string {
	return s.ID[0:strings.Index(s.ID, "-")]
}

// Environment returns the servers environment
func (s *ServerStruct) Environment() (Environment, error) {
	var err error
	if s.environment == nil {
		switch s.Service().EnvironmentName {
		case "docker":
			s.environment, err = NewDockerEnvironment(s)
		default:
			log.WithField("service", s.ServiceName).Error("Invalid environment name")
			return nil, errors.New("Invalid environment name")
		}
	}
	return s.environment, err
}

// HasPermission checks wether a provided token has a specific permission
func (s *ServerStruct) HasPermission(token string, permission string) bool {
	for key, perms := range s.Keys {
		if key == token {
			for _, perm := range perms {
				if perm == permission || perm == "s:*" {
					return true
				}
			}
			return false
		}
	}
	return false
}

func (s *ServerStruct) Save() error {
	if err := storeServerConfiguration(s); err != nil {
		log.WithField("server", s.ID).WithError(err).Error("Failed to store server configuration.")
		return err
	}
	return nil
}

func (s *ServerStruct) path() string {
	return filepath.Join(viper.GetString(config.DataPath), constants.ServersPath, s.ID)
}

func (s *ServerStruct) dataPath() string {
	return filepath.Join(s.path(), constants.ServerDataPath)
}

func (s *ServerStruct) configFilePath() string {
	return filepath.Join(s.path(), constants.ServerConfigFile)
}
