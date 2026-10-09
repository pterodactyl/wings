package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// An XML file nested deeper than maxXMLDepth is left as it is.
func TestNestedXMLOverDepthLimitIsNotRewritten(t *testing.T) {
	const depth = 10000
	content := strings.Repeat("<a>", depth) + strings.Repeat("</a>", depth)
	path := filepath.Join(t.TempDir(), "config.xml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	var cf ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{"file":"config.xml","parser":"xml","replace":[{"match":"a","replace_with":"value"}]}`), &cf))
	cf.configuration = []byte(`{}`)

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err)
	defer f.Close()

	start := time.Now()
	require.Error(t, cf.parseXmlFile(f))
	require.Less(t, time.Since(start), 5*time.Second)

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, content, string(b))
}
