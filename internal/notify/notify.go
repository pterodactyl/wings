// Package notify handles notifying the operating system of the program's state.
//
// For linux based operating systems, this is done through the systemd socket
// set by "NOTIFY_SOCKET" environment variable.
//
// Currently, no other operating systems are supported.
package notify

func Readiness() error {
	return readiness()
}

func Reloading() error {
	return reloading()
}

func Stopping() error {
	return stopping()
}
