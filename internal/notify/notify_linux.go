package notify

import (
	"io"
	"net"
	"os"
	"strings"
)

func notify(path string, r io.Reader) error {
	s := &net.UnixAddr{
		Name: path,
		Net:  "unixgram",
	}
	c, err := net.DialUnix(s.Net, nil, s)
	if err != nil {
		return err
	}
	defer c.Close()

	if _, err := io.Copy(c, r); err != nil {
		return err
	}
	return nil
}

func socketNotify(payload string) error {
	v, ok := os.LookupEnv("NOTIFY_SOCKET")
	if !ok || v == "" {
		return nil
	}
	if err := notify(v, strings.NewReader(payload)); err != nil {
		return err
	}
	return nil
}

func readiness() error {
	return socketNotify("READY=1")
}

func reloading() error {
	return socketNotify("RELOADING=1")
}

func stopping() error {
	return socketNotify("STOPPING=1")
}
