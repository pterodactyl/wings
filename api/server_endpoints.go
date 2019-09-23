package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/buger/jsonparser"
	"go.uber.org/zap"
)

const (
	ProcessStopCommand = "command"
	ProcessStopSignal = "signal"
	ProcessStopNativeStop = "stop"
)

// Defines a single find/replace instance for a given server configuration file.
type ConfigurationFileReplacement struct {
	Match     string               `json:"match"`
	Value     string               `json:"value"`
	ValueType jsonparser.ValueType `json:"-"`
}

func (cfr *ConfigurationFileReplacement) UnmarshalJSON(data []byte) error {
	if m, err := jsonparser.GetString(data, "match"); err != nil {
		return err
	} else {
		cfr.Match = m
	}

	if v, dt, _, err := jsonparser.Get(data, "value"); err != nil {
		return err
	} else {
		if dt != jsonparser.String && dt != jsonparser.Number && dt != jsonparser.Boolean {
			return errors.New(
				fmt.Sprintf("cannot parse JSON: received unexpected replacement value type: %d", dt),
			)
		}

		cfr.Value = string(v)
		cfr.ValueType = dt
	}

	return nil
}

// Defines a configuration file for the server startup. These will be looped over
// and modified before the server finishes booting.
type ConfigurationFile struct {
	FileName string                         `json:"file"`
	Parser   string                         `json:"parser"`
	Replace  []ConfigurationFileReplacement `json:"replace"`
}

// Defines the process configuration for a given server instance. This sets what the
// daemon is looking for to mark a server as done starting, what to do when stopping,
// and what changes to make to the configuration file for a server.
type ServerConfiguration struct {
	Startup struct {
		Done            string   `json:"done"`
		UserInteraction []string `json:"userInteraction"`
	} `json:"startup"`
	Stop struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"stop"`
	ConfigurationFiles []ConfigurationFile `json:"configs"`
}

// Fetches the server configuration and returns the struct for it.
func (r *PanelRequest) GetServerConfiguration(uuid string) (*ServerConfiguration, error) {
	resp, err := r.Get(fmt.Sprintf("/servers/%s/configuration", uuid))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	r.Response = resp

	if r.HasError() {
		e, err := r.Error()
		zap.S().Warnw("got error", zap.String("message", e), zap.Error(err))

		return nil, err
	}

	res := &ServerConfiguration{}
	b, _ := r.ReadBody()

	if err := json.Unmarshal(b, res); err != nil {
		return nil, err
	}

	return res, nil
}
