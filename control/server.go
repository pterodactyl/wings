package control

import (
	"errors"

	"github.com/pterodactyl/wings/api/websockets"
	log "github.com/sirupsen/logrus"
)

type Status string

const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
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
	Websockets() *websockets.Collection

	HasPermission(string, string) bool
}

// ServerStruct is a single instance of a Service managed by the panel
type ServerStruct struct {
	// ID is the unique identifier of the server
	ID string `json:"uuid" jsonapi:"primary,server"`

	// ServiceName is the name of the service. It is mainly used to allow storing the service
	// in the config
	ServiceName string   `json:"serviceName"`
	Service     *Service `json:"-" jsonapi:"relation,service"`
	environment Environment

	// StartupCommand is the command executed in the environment to start the server
	StartupCommand string `json:"startupCommand" jsonapi:"attr,startup_command"`

	// DockerContainer holds information regarding the docker container when the server
	// is running in a docker environment
	DockerContainer dockerContainer `json:"dockerContainer" jsonapi:"attr,docker_container"`

	// EnvironmentVariables are set in the Environment the server is running in
	EnvironmentVariables map[string]string `json:"environmentVariables" jsonapi:"attr,environment_variables"`

	// Allocations contains the ports and ip addresses assigned to the server
	Allocations allocations `json:"allocation" jsonapi:"attr,allocations"`

	// Settings are the environment settings and limitations for the server
	Settings settings `json:"settings" jsonapi:"attr,settings"`

	// Keys are some auth keys we will hopefully replace by something better.
	// TODO remove
	Keys map[string][]string `json:"keys"`

	websockets *websockets.Collection
	status     Status
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
		delete(servers, server.ID)
		return nil, err
	}
	if err := server.init(); err != nil {
		DeleteServer(server.ID)
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

func (s *ServerStruct) init() error {
	// TODO: Properly use the correct service, mock for now.
	s.Service = &Service{
		DockerImage:     "quay.io/pterodactyl/core:java",
		EnvironmentName: "docker",
	}
	s.status = StatusStopped

	s.websockets = websockets.NewCollection()
	go s.websockets.Run()

	var err error
	if s.environment == nil {
		switch s.GetService().EnvironmentName {
		case "docker":
			s.environment, err = NewDockerEnvironment(s)
		default:
			log.WithField("service", s.ServiceName).Error("Invalid environment name")
			return errors.New("Invalid environment name")
		}
	}
	return err
}

func (s *ServerStruct) Start() error {
	s.SetStatus(StatusStarting)
	env, err := s.Environment()
	if err != nil {
		s.SetStatus(StatusStopped)
		return err
	}
	if !env.Exists() {
		if err := env.Create(); err != nil {
			s.SetStatus(StatusStopped)
			return err
		}
	}
	return env.Start()
}

func (s *ServerStruct) Stop() error {
	s.SetStatus(StatusStopping)
	env, err := s.Environment()
	if err != nil {
		s.SetStatus(StatusRunning)
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
