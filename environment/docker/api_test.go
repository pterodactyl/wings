package docker

import (
	"net/http"
	"testing"

	"emperror.dev/errors"
	cerrdefs "github.com/containerd/errdefs"
)

func TestStatusError(t *testing.T) {
	base := errors.New("Error response from daemon: no such container")

	err := errors.WithStack(statusError(base, http.StatusNotFound))
	if !cerrdefs.IsNotFound(err) {
		t.Errorf("expected a 404 to be reported as not found")
	}
	if err.Error() != base.Error() {
		t.Errorf("expected message %q, got %q", base.Error(), err.Error())
	}
	if !errors.Is(err, base) {
		t.Errorf("expected the original error to be preserved")
	}

	for _, code := range []int{http.StatusBadRequest, http.StatusConflict, http.StatusInternalServerError} {
		if cerrdefs.IsNotFound(statusError(base, code)) {
			t.Errorf("expected status %d not to be reported as not found", code)
		}
	}

	if !cerrdefs.IsConflict(statusError(base, http.StatusConflict)) {
		t.Errorf("expected a 409 to be reported as a conflict")
	}

	if statusError(nil, http.StatusNotFound) != nil {
		t.Errorf("expected a nil error to stay nil")
	}
}
