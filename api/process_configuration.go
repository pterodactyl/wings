package api

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/apex/log"
	"github.com/pterodactyl/wings/parser"
)

type OutputLineMatcher struct {
	// The raw string to match against. This may or may not be prefixed with
	// regex: which indicates we want to match against the regex expression.
	raw string
	reg *regexp.Regexp
}

// Determine if a given string "s" matches the given line.
func (olm *OutputLineMatcher) Matches(s string) bool {
	if olm.reg == nil {
		return strings.Contains(s, olm.raw)
	}

	return olm.reg.MatchString(s)
}

// Return the matcher's raw comparison string.
func (olm *OutputLineMatcher) String() string {
	return olm.raw
}

// Unmarshal the startup lines into individual structs for easier matching abilities.
func (olm *OutputLineMatcher) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &olm.raw); err != nil {
		return err
	}

	if strings.HasPrefix(olm.raw, "regex:") && len(olm.raw) > 6 {
		r, err := regexp.Compile(strings.TrimPrefix(olm.raw, "regex:"))
		if err != nil {
			log.WithField("error", err).WithField("raw", olm.raw).Warn("failed to compile output line marked as being regex")
		}

		olm.reg = r
	}

	return nil
}

type ProcessStopConfiguration struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Defines the process configuration for a given server instance. This sets what the
// daemon is looking for to mark a server as done starting, what to do when stopping,
// and what changes to make to the configuration file for a server.
type ProcessConfiguration struct {
	Startup struct {
		Done            []*OutputLineMatcher `json:"done"`
		UserInteraction []string             `json:"user_interaction"`
		StripAnsi       bool                 `json:"strip_ansi"`
	} `json:"startup"`

	Stop ProcessStopConfiguration `json:"stop"`

	ConfigurationFiles []parser.ConfigurationFile `json:"configs"`
}
