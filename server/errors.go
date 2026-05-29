package server

import (
	"emperror.dev/errors"
)

var (
	ErrIsRunning            = errors.New("server is running")
	ErrSuspended            = errors.New("server is currently in a suspended state")
	ErrServerIsInstalling   = errors.New("server is currently installing")
	ErrServerIsTransferring = errors.New("server is currently being transferred")
	ErrServerIsRestoring    = errors.New("server is currently being restored")
)

type crashTooFrequent struct{}

func (e *crashTooFrequent) Error() string {
	return "server has crashed too soon after the last detected crash"
}

func IsTooFrequentCrashError(err error) bool {
	_, ok := err.(*crashTooFrequent)

	return ok
}

type serverDoesNotExist struct{}

func (e *serverDoesNotExist) Error() string {
	return "server does not exist on remote system"
}

func IsServerDoesNotExistError(err error) bool {
	_, ok := err.(*serverDoesNotExist)

	return ok
}
