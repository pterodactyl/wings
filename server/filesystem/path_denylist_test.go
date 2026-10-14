package filesystem

import (
	"testing"
)

func TestIsIgnoredMatchesTheResolvedPath(t *testing.T) {
	NewFs()
	fs, err := New(t.TempDir(), 0, []string{"/server.jar", "config/"})
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		"server.jar", "/server.jar", "./server.jar", "/./server.jar", "//server.jar",
		"/sub/../server.jar", "../server.jar", "config/a.yml", "/x/../config/a.yml",
		"/config/", "config/", "/x/../config/",
	} {
		if err := fs.IsIgnored(p); !IsErrorCode(err, ErrCodeDenylistFile) {
			t.Errorf("expected %q to be denied, got %v", p, err)
		}
	}
	for _, p := range []string{"sub/server.jar", "/server.jar.bak", "/configs/a.yml"} {
		if err := fs.IsIgnored(p); err != nil {
			t.Errorf("expected %q to be allowed, got %v", p, err)
		}
	}
}
