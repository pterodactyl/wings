package server

type suspendedError struct {
}

func (e *suspendedError) Error() string {
	return "server is currently in a suspended state"
}

func IsSuspendedError(err error) bool {
	_, ok := err.(*suspendedError)

	return ok
}