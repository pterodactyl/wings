package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/environment"
	wserver "github.com/pterodactyl/wings/server"
)

// powerTestEnvironment reports each stop on stops and holds it until release is closed.
type powerTestEnvironment struct {
	environment.ProcessEnvironment
	stops   chan struct{}
	release chan struct{}
}

func (e *powerTestEnvironment) WaitForStop(context.Context, time.Duration, bool) error {
	e.stops <- struct{}{}
	<-e.release
	return nil
}

// A stop sent without wait_seconds while another power action holds the lock runs
// once the lock is released, instead of being dropped after the 202 response.
func TestStopWithoutWaitSecondsWaitsForPowerLock(t *testing.T) {
	s, err := wserver.New(nil)
	require.NoError(t, err)
	t.Cleanup(s.CtxCancel)
	env := &powerTestEnvironment{stops: make(chan struct{}, 2), release: make(chan struct{})}
	s.Environment = env

	go func() { _ = s.HandlePowerAction(wserver.PowerActionStop) }()
	<-env.stops

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/servers/test/power", strings.NewReader(`{"action":"stop"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("server", s)
	c.Set("logger", log.WithField("test", t.Name()))

	postServerPower(c)
	require.Equal(t, http.StatusAccepted, c.Writer.Status())

	select {
	case <-env.stops:
		t.Fatal("the stop ran while another power action held the lock")
	case <-time.After(100 * time.Millisecond):
	}

	close(env.release)

	select {
	case <-env.stops:
	case <-time.After(2 * time.Second):
		t.Fatal("the stop was dropped instead of waiting for the power lock")
	}
}
