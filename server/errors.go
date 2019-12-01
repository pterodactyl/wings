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

type crashTooFrequent struct {
}

func (e *crashTooFrequent) Error() string {
	return "server has crashed too soon after the last detected crash"
}

func IsTooFrequentCrashError(err error) bool {
	_, ok := err.(*crashTooFrequent)

	return ok
}
