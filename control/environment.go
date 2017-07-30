package control

// Environment provides abstraction of different environments
type Environment interface {
	// Create creates the environment
	Create() error

	// Destroy destroys the environment
	Destroy() error

	// Start starts the service in the environment
	Start() error

	// Stop stops the service in the environment
	Stop() error

	// Kill kills the service in the environment
	Kill() error

	// Execute a command in the environment
	// This sends the command to the standard input of the environment
	Exec(command string) error

	// Exists checks wether the Environment exists or not
	Exists() bool

	// ReCreate recreates the environment by first Destroying and then Creating
	ReCreate() error
}

type baseEnvironment struct {
}

// Ensure BaseEnvironment implements Environment
var _ Environment = &baseEnvironment{}

func (env *baseEnvironment) Create() error {
	return nil
}

func (env *baseEnvironment) Destroy() error {
	return nil
}

func (env *baseEnvironment) Start() error {
	return nil
}

func (env *baseEnvironment) Stop() error {
	return nil
}

func (env *baseEnvironment) Kill() error {
	return nil
}

func (env *baseEnvironment) Exists() bool {
	return false
}

func (env *baseEnvironment) ReCreate() error {
	if env.Exists() {
		if err := env.Destroy(); err != nil {
			return err
		}
	}
	return env.Create()
}

func (env *baseEnvironment) Exec(command string) error {
	return nil
}
