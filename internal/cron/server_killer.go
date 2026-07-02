package cron

import (
	"context"

	"emperror.dev/errors"
	"github.com/apex/log"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

type serverKillerCron struct {
	mu *system.AtomicBool
	resourceKiller
}

func newServerKillerCron(m *server.Manager) *serverKillerCron {
	return &serverKillerCron{
		mu:             system.NewAtomicBool(false),
		resourceKiller: resourceKiller{manager: m},
	}
}

// Run evaluates disk usage thresholds and kills/stops running server containers
// when they are crossed.
func (sk *serverKillerCron) Run(ctx context.Context) error {
	if !sk.mu.SwapIf(true) {
		return errors.WithStack(ErrCronRunning)
	}
	defer sk.mu.Store(false)

	cfg := config.Get().System.ServerKiller
	if !cfg.Enabled {
		return nil
	}
	if !cfg.IsValid() {
		log.Warn("server killer: configuration is invalid, skipping")
		return nil
	}

	path := cfg.Path
	if path == "" {
		path = config.Get().System.Data
	}

	used, _, _, err := system.DiskUsagePercent(path)
	if err != nil {
		return errors.Wrap(err, "server killer: failed to get disk usage")
	}

	tgCfg := config.Get().System.TelegramNotifications.AsSystemConfig()
	return sk.evaluate(ctx, cfg, used, "disk", tgCfg)
}
