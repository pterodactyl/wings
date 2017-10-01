package control

type Service struct {
	server *Server

	// EnvironmentName is the name of the environment used by the service
	EnvironmentName string `json:"environmentName"`

	DockerImage string `json:"dockerImage"`
}
