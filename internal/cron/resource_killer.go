package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

// resourceKiller contains the shared logic for stopping/killing running servers
// when a system resource (disk, memory, etc.) crosses a threshold.
type resourceKiller struct {
	manager *server.Manager
}

// evaluate compares the current resource usage against the configured soft and
// hard thresholds and applies the configured action to the selected servers.
func (rk *resourceKiller) evaluate(ctx context.Context, cfg config.ResourceKillerConfig, used float64, resource string, tgCfg system.TelegramConfig) error {
	if cfg.Hard.Threshold > 0 && used >= float64(cfg.Hard.Threshold) {
		log.WithFields(log.Fields{
			"resource":  resource,
			"used":      used,
			"threshold": cfg.Hard.Threshold,
			"action":    cfg.Hard.Action,
		}).Warnf("%s killer: hard threshold reached, acting on all running servers", resource)
		return rk.actOnServers(ctx, cfg.Hard, rk.runningServers(), resource, used, tgCfg)
	}

	if cfg.Soft.Threshold > 0 && used >= float64(cfg.Soft.Threshold) {
		log.WithFields(log.Fields{
			"resource":  resource,
			"used":      used,
			"threshold": cfg.Soft.Threshold,
			"count":     cfg.Soft.Count,
			"action":    cfg.Soft.Action,
			"order":     cfg.Soft.Order,
		}).Warnf("%s killer: soft threshold reached, acting on selected running servers", resource)
		servers := rk.selectServers(cfg.Soft.Count, cfg.Soft.Order)
		return rk.actOnServers(ctx, cfg.Soft, servers, resource, used, tgCfg)
	}

	return nil
}

// runningServers returns all servers that are currently running or starting.
func (rk *resourceKiller) runningServers() []*server.Server {
	var out []*server.Server
	for _, s := range rk.manager.All() {
		state := s.Environment.State()
		if state == environment.ProcessRunningState || state == environment.ProcessStartingState {
			out = append(out, s)
		}
	}
	return out
}

// selectServers returns up to count running servers sorted by the configured
// order (oldest/newest based on data directory mtime).
func (rk *resourceKiller) selectServers(count int, order string) []*server.Server {
	servers := rk.runningServers()
	if len(servers) == 0 {
		return nil
	}

	type candidate struct {
		server *server.Server
		mtime  time.Time
	}

	var candidates []candidate
	dataRoot := config.Get().System.Data
	for _, s := range servers {
		// Skip servers that are mid-operation to avoid corrupting installs,
		// restores, or transfers.
		if s.IsInstalling() || s.IsRestoring() || s.IsTransferring() {
			continue
		}

		p := filepath.Join(dataRoot, s.ID())
		info, err := os.Stat(p)
		if err != nil {
			log.WithError(err).WithField("server", s.ID()).Warn("server killer: failed to stat server data directory")
			continue
		}

		candidates = append(candidates, candidate{
			server: s,
			mtime:  info.ModTime(),
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if order == "newest" {
			return candidates[i].mtime.After(candidates[j].mtime)
		}
		return candidates[i].mtime.Before(candidates[j].mtime)
	})

	if count < 0 || count > len(candidates) {
		count = len(candidates)
	}

	out := make([]*server.Server, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, candidates[i].server)
	}
	return out
}

// actOnServers applies the configured action to each selected server in a
// goroutine with a bounded timeout.
func (rk *resourceKiller) actOnServers(ctx context.Context, threshold config.ResourceKillerThreshold, servers []*server.Server, resource string, used float64, tgCfg system.TelegramConfig) error {
	if len(servers) == 0 {
		return nil
	}

	// Give the whole batch a reasonable window. Each server also gets its own
	// timeout inside applyAction.
	actx, cancel := context.WithTimeout(ctx, time.Minute*5)
	defer cancel()

	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Add(1)
		go func(srv *server.Server) {
			defer wg.Done()
			if err := rk.applyAction(actx, srv, threshold.Action); err != nil {
				log.WithError(err).WithField("server", srv.ID()).Error("server killer: failed to act on server")
			}
		}(s)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-actx.Done():
		log.Warn("server killer: batch action timed out before all servers were handled")
	}

	level := "soft"
	if threshold.Count < 0 {
		level = "hard"
	}

	log.WithFields(log.Fields{
		"action":  threshold.Action,
		"servers": len(servers),
	}).Info("server killer: finished acting on servers")

	msg := fmt.Sprintf(
		"⚠️ *%s Killer: %s*\nUsage: %.1f%%\nThreshold: %d%%\nAction: `%s`\nServers affected: %d",
		capitalize(resource), capitalize(level), used, threshold.Threshold, threshold.Action, len(servers),
	)
	system.SendTelegramNotificationAsync(tgCfg, msg)

	return nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}

// applyAction applies a single power action to a server.
func (rk *resourceKiller) applyAction(ctx context.Context, s *server.Server, action string) error {
	log.WithFields(log.Fields{
		"server": s.ID(),
		"action": action,
	}).Warn("server killer: acting on server")

	switch action {
	case "kill":
		return s.Environment.Terminate(s.Context(), "SIGKILL")
	case "stop":
		// Gracefully stop the server, but force-kill if it does not stop within
		// the remaining context time.
		timeout := time.Minute
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining > 0 && remaining < timeout {
				timeout = remaining
			}
		}
		return s.Environment.WaitForStop(s.Context(), timeout, true)
	default:
		return errors.Errorf("server killer: unknown action %q", action)
	}
}
