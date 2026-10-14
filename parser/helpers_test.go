package parser

import (
	"encoding/json"
	"testing"

	"github.com/Jeffail/gabs/v2"
	"github.com/buger/jsonparser"
)

func replacement(t *testing.T, raw string) ConfigurationFileReplacement {
	t.Helper()

	var cfr ConfigurationFileReplacement
	if err := json.Unmarshal([]byte(raw), &cfr); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}

	return cfr
}

func TestSetAtPathway(t *testing.T) {
	cases := []struct {
		name        string
		document    string
		replacement string
		value       string
		path        string
		want        string
	}{
		{
			name:        "no if_value replaces",
			document:    `{"server":{"port":25565}}`,
			replacement: `{"match":"server.port","replace_with":"25566"}`,
			value:       "25566",
			path:        "server.port",
			want:        "25566",
		},
		{
			name:        "if_value matches the key's value",
			document:    `{"listeners":{"host":"0.0.0.0:25577","motd":"hi"}}`,
			replacement: `{"match":"listeners.host","if_value":"0.0.0.0:25577","replace_with":"0.0.0.0:30000"}`,
			value:       "0.0.0.0:30000",
			path:        "listeners.host",
			want:        `"0.0.0.0:30000"`,
		},
		{
			name:        "if_value does not match",
			document:    `{"listeners":{"host":"127.0.0.1:25577"}}`,
			replacement: `{"match":"listeners.host","if_value":"0.0.0.0:25577","replace_with":"0.0.0.0:30000"}`,
			value:       "0.0.0.0:30000",
			path:        "listeners.host",
			want:        `"127.0.0.1:25577"`,
		},
		{
			name:        "numeric if_value matches a numeric value",
			document:    `{"server":{"port":25565}}`,
			replacement: `{"match":"server.port","if_value":25565,"replace_with":"30000"}`,
			value:       "30000",
			path:        "server.port",
			want:        "30000",
		},
		{
			name:        "regex if_value rewrites an existing value",
			document:    `{"servers":{"lobby":{"address":"localhost:25565"}}}`,
			replacement: `{"match":"servers.lobby.address","if_value":"regex:^(127\\.0\\.0\\.1|localhost)(:\\d{1,5})?$","replace_with":"10.0.0.5$2"}`,
			value:       "10.0.0.5$2",
			path:        "servers.lobby.address",
			want:        `"10.0.0.5:25565"`,
		},
		{
			name:        "regex if_value leaves a non-matching value",
			document:    `{"servers":{"lobby":{"address":"play.example.com:25565"}}}`,
			replacement: `{"match":"servers.lobby.address","if_value":"regex:^(127\\.0\\.0\\.1|localhost)(:\\d{1,5})?$","replace_with":"10.0.0.5$2"}`,
			value:       "10.0.0.5$2",
			path:        "servers.lobby.address",
			want:        `"play.example.com:25565"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := gabs.ParseJSON([]byte(tc.document))
			if err != nil {
				t.Fatal(err)
			}

			cfr := replacement(t, tc.replacement)
			if err := cfr.SetAtPathway(c, cfr.Match, tc.value); err != nil {
				t.Fatalf("SetAtPathway: %v", err)
			}
			if got := c.Path(tc.path).String(); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSetAtPathwayRegexOnMissingKey(t *testing.T) {
	c, _ := gabs.ParseJSON([]byte(`{"servers":{}}`))
	cfr := replacement(t, `{"match":"servers.lobby.address","if_value":"regex:^localhost","replace_with":"10.0.0.5"}`)

	if err := cfr.SetAtPathway(c, cfr.Match, "10.0.0.5"); err != gabs.ErrNotFound {
		t.Fatalf("expected gabs.ErrNotFound for a missing key, got %v", err)
	}
	if c.ExistsP("servers.lobby.address") {
		t.Fatal("a regex replacement must not create a missing key")
	}
}

func TestReplacementAcceptsNumericKeys(t *testing.T) {
	cfr := replacement(t, `{"match":25565,"if_value":true,"replace_with":"{{server.build.default.port}}"}`)

	if cfr.Match != "25565" {
		t.Fatalf("match: got %q", cfr.Match)
	}
	if cfr.IfValue != "true" {
		t.Fatalf("if_value: got %q", cfr.IfValue)
	}
	if cfr.ReplaceWith.Type() != jsonparser.String {
		t.Fatalf("replace_with type: got %v", cfr.ReplaceWith.Type())
	}
}

func TestConfigurationFileKeepsReplacementsWithNumericMatch(t *testing.T) {
	var f ConfigurationFile
	raw := `{"file":"server.properties","parser":"properties","replace":[{"match":25565,"replace_with":"a"},{"match":"motd","replace_with":"b"}]}`
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Replace) != 2 {
		t.Fatalf("expected both replacements to survive decoding, got %d", len(f.Replace))
	}
}
