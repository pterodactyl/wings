//go:build !linux
// +build !linux

package notify

func readiness() error {
	return nil
}

func reloading() error {
	return nil
}

func stopping() error {
	return nil
}
