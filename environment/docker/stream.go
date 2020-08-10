package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"github.com/docker/docker/api/types"
	"github.com/pkg/errors"
	"io"
	"os"
)

type dockerLogLine struct {
	Log string `json:"log"`
}

func (d *Environment) setStream(s *types.HijackedResponse) {
	d.mu.Lock()
	d.stream = s
	d.mu.Unlock()
}

// Sends the specified command to the stdin of the running container instance. There is no
// confirmation that this data is sent successfully, only that it gets pushed into the stdin.
func (d *Environment) SendCommand(c string) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.IsAttached() {
		return errors.New("attempting to send command to non-attached instance")
	}

	_, err := d.stream.Conn.Write([]byte(c + "\n"))

	return errors.WithStack(err)
}

// Reads the log file for the server. This does not care if the server is running or not, it will
// simply try to read the last X bytes of the file and return them.
func (d *Environment) Readlog(len int64) ([]string, error) {
	j, err := d.client.ContainerInspect(context.Background(), d.Id)
	if err != nil {
		return nil, err
	}

	if j.LogPath == "" {
		return nil, errors.New("empty log path defined for server")
	}

	f, err := os.Open(j.LogPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Check if the length of the file is smaller than the amount of data that was requested
	// for reading. If so, adjust the length to be the total length of the file. If this is not
	// done an error is thrown since we're reading backwards, and not forwards.
	if stat, err := os.Stat(j.LogPath); err != nil {
		return nil, err
	} else if stat.Size() < len {
		len = stat.Size()
	}

	// Seed to the end of the file and then move backwards until the length is met to avoid
	// reading the entirety of the file into memory.
	if _, err := f.Seek(-len, io.SeekEnd); err != nil {
		return nil, err
	}

	b := make([]byte, len)

	if _, err := f.Read(b); err != nil && err != io.EOF {
		return nil, err
	}

	return d.parseLogToStrings(b)
}

// Docker stores the logs for server output in a JSON format. This function will iterate over the JSON
// that was read from the log file and parse it into a more human readable format.
func (d *Environment) parseLogToStrings(b []byte) ([]string, error) {
	var hasError = false
	var out []string

	scanner := bufio.NewScanner(bytes.NewReader(b))
	for scanner.Scan() {
		var l dockerLogLine
		// Unmarshal the contents and allow up to a single error before bailing out of the process. We
		// do this because if you're arbitrarily reading a length of the file you'll likely end up
		// with the first line in the output being improperly formatted JSON. In those cases we want to
		// just skip over it. However if we see another error we're going to bail out because that is an
		// abnormal situation.
		if err := json.Unmarshal([]byte(scanner.Text()), &l); err != nil {
			if hasError {
				return nil, err
			}

			hasError = true
			continue
		}

		out = append(out, l.Log)
	}

	return out, nil
}
