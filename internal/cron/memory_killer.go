package cron

import (
	"context"

	"emperror.dev/errors"
	"github.com/apex/log"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

type memoryKillerCron struct {
	mu *system.AtomicBool
	resourceKiller
}

func newMemoryKillerCron(m *server.Manager) *memoryKillerCron {
	return &memoryKillerCron{
		mu:             system.NewAtomicBool(false),
		resourceKiller: resourceKiller{manager: m},
	}
}

// Run evaluates system memory usage thresholds and kills/stops running server
// containers when they are crossed.
func (mk *memoryKillerCron) Run(ctx context.Context) error {
	if !mk.mu.SwapIf(true) {
		return errors.WithStack(ErrCronRunning)
	}
	defer mk.mu.Store(false)

	cfg := config.Get().System.MemoryKiller
	if !cfg.Enabled {
		return nil
	}
	if !cfg.IsValid() {
		log.Warn("memory killer: configuration is invalid, skipping")
		return nil
	}

	used, _, _, err := system.MemoryUsagePercent()
	if err != nil {
		return errors.Wrap(err, "memory killer: failed to get memory usage")
	}

	tgCfg := config.Get().System.TelegramNotifications.AsSystemConfig()
	return mk.evaluate(ctx, cfg, used, "memory", tgCfg)
}
