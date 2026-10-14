package remote

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutputLineMatcher(t *testing.T) {
	for _, tt := range []struct {
		raw   string
		line  string
		match bool
	}{
		{raw: "Done", line: "[12:00] Done (1.2s)!", match: true},
		{raw: "Done", line: "Loading", match: false},
		{raw: `regex:^\[\d+:\d+\] Done`, line: "[12:00] Done (1.2s)!", match: true},
		{raw: `regex:^Done`, line: "[12:00] Done", match: false},
		// An expression that does not compile is matched as plain text.
		{raw: "regex:Done (", line: "[12:00] Done (1.2s)!", match: true},
		{raw: "regex:Done (", line: "[12:00] Loading", match: false},
	} {
		var m OutputLineMatcher
		b, err := json.Marshal(tt.raw)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &m))
		require.Equal(t, tt.match, m.Matches([]byte(tt.line)), "%q against %q", tt.raw, tt.line)
		require.Equal(t, tt.raw, m.String())
	}
}
