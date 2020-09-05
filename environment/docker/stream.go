package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"github.com/docker/docker/api/types"
	"github.com/pkg/errors"
	"strconv"
)

type dockerLogLine struct {
	Log string `json:"log"`
}

func (e *Environment) setStream(s *types.HijackedResponse) {
	e.mu.Lock()
	e.stream = s
	e.mu.Unlock()
}

// Sends the specified command to the stdin of the running container instance. There is no
// confirmation that this data is sent successfully, only that it gets pushed into the stdin.
func (e *Environment) SendCommand(c string) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.IsAttached() {
		return errors.New("attempting to send command to non-attached instance")
	}

	_, err := e.stream.Conn.Write([]byte(c + "\n"))

	return errors.WithStack(err)
}

// Reads the log file for the server. This does not care if the server is running or not, it will
// simply try to read the last X bytes of the file and return them.
func (e *Environment) Readlog(lines int) ([]string, error) {
	r, err := e.client.ContainerLogs(context.Background(), e.Id, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       strconv.Itoa(lines),
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer r.Close()

	var out []string

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}

	return out, nil
}

// Docker stores the logs for server output in a JSON format. This function will iterate over the JSON
// that was read from the log file and parse it into a more human readable format.
func (e *Environment) parseLogToStrings(b []byte) ([]string, error) {
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
