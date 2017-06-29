package environments

// Environment provides abstraction of different environments
type Environment interface {
	// Execute a command in the environment
	Exec() error

	// Create creates the environment
	Create() error

	// Destroy destroys the environment
	Destroy() error

	// Exists checks wether the Environment exists or not
	Exists() bool

	// ReCreate recreates the environment by first Destroying and then Creating
	ReCreate() error
}

type BaseEnvironment struct {
}

// Ensure BaseEnvironment implements Environment
var _ Environment = &BaseEnvironment{}

func (env *BaseEnvironment) Create() error {
	return nil
}

func (env *BaseEnvironment) Destroy() error {
	return nil
}

func (env *BaseEnvironment) Exists() bool {
	return false
}

func (env *BaseEnvironment) ReCreate() error {
	if env.Exists() {
		if err := env.Destroy(); err != nil {
			return err
		}
	}
	return env.Create()
}

func (env *BaseEnvironment) Exec() error {
	return nil
}
