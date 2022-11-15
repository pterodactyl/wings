package installer

type validationError struct {
	msg string
}

func (e *validationError) Error() string {
	return e.msg
}

func IsValidationError(err error) bool {
	_, ok := err.(*validationError)

	return ok
}

func NewValidationError(msg string) error {
	return &validationError{msg: msg}
}
