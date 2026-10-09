package parser

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Control: a present key is substituted into the placeholder.
func TestPresentConfigurationKeySubstitutes(t *testing.T) {
	f := ConfigurationFile{configuration: []byte(`{"docker":{"interface":"eth0"}}`)}
	cfr := replacement(t, `{"match":"bind-address","replace_with":"{{config.docker.interface}}"}`)

	got, err := f.LookupConfigurationValue(cfr)
	require.NoError(t, err)
	assert.Equal(t, "eth0", got)
}

// A placeholder for a key that does not exist is left in place.
func TestMissingConfigurationKeyKeepsPlaceholder(t *testing.T) {
	f := ConfigurationFile{configuration: []byte(`{"docker":{"interface":"eth0"}}`)}
	cfr := replacement(t, `{"match":"bind-address","replace_with":"{{config.docker.missing}}"}`)

	got, err := f.LookupConfigurationValue(cfr)
	require.NoError(t, err)
	assert.Equal(t, "{{config.docker.missing}}", got, "a missing key should leave the placeholder in place")
}

// A configuration file entry that is missing a field is refused when it is decoded.
func TestConfigurationFileWithoutParserKeyReturnsError(t *testing.T) {
	var f ConfigurationFile
	var err error
	require.NotPanics(t, func() {
		err = json.Unmarshal([]byte(`{"file":"server.properties","replace":[]}`), &f)
	})
	require.Error(t, err)

	for _, raw := range []string{`{"parser":"file","replace":[]}`, `{"file":"a","parser":"file"}`} {
		require.NotPanics(t, func() {
			err = json.Unmarshal([]byte(raw), &f)
		})
		require.Error(t, err, raw)
	}
}

// An invalid escape in replace_with is refused while the document is validated,
// before any replacement value is built.
func TestInvalidReplacementEscapeIsRejectedBeforeDecoding(t *testing.T) {
	var f ConfigurationFile
	var err error
	require.NotPanics(t, func() {
		err = json.Unmarshal([]byte(`{"file":"a","parser":"file","replace":[{"match":"k","replace_with":"\z"}]}`), &f)
	})
	require.Error(t, err)
}
