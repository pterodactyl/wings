package cron

import (
	"context"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/go-co-op/gocron"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

const ErrCronRunning = errors.Sentinel("cron: job already running")

var o system.AtomicBool

// Scheduler configures the internal cronjob system for Wings and returns the scheduler
// instance to the caller. This should only be called once per application lifecycle, additional
// calls will result in an error being returned.
func Scheduler(ctx context.Context, m *server.Manager) (*gocron.Scheduler, error) {
	if !o.SwapIf(true) {
		return nil, errors.New("cron: cannot call scheduler more than once in application lifecycle")
	}
	location, err := time.LoadLocation(config.Get().System.Timezone)
	if err != nil {
		return nil, errors.Wrap(err, "cron: failed to parse configured system timezone")
	}

	activity := activityCron{
		mu:      system.NewAtomicBool(false),
		manager: m,
		max:     config.Get().System.ActivitySendCount,
	}

	sftp := sftpCron{
		mu:      system.NewAtomicBool(false),
		manager: m,
		max:     config.Get().System.ActivitySendCount,
	}

	diskCleanup := newDiskCleanupCron(m)
	serverKiller := newServerKillerCron(m)
	memoryKiller := newMemoryKillerCron(m)

	s := gocron.NewScheduler(location)
	l := log.WithField("subsystem", "cron")

	interval := time.Duration(config.Get().System.ActivitySendInterval) * time.Second
	l.WithField("interval", interval).Info("configuring system crons")

	_, _ = s.Tag("activity").Every(interval).Do(func() {
		l.WithField("cron", "activity").Debug("sending internal activity events to Panel")
		if err := activity.Run(ctx); err != nil {
			if errors.Is(err, ErrCronRunning) {
				l.WithField("cron", "activity").Warn("activity process is already running, skipping...")
			} else {
				l.WithField("cron", "activity").WithField("error", err).Error("activity process failed to execute")
			}
		}
	})

	_, _ = s.Tag("sftp").Every(interval).Do(func() {
		l.WithField("cron", "sftp").Debug("sending sftp events to Panel")
		if err := sftp.Run(ctx); err != nil {
			if errors.Is(err, ErrCronRunning) {
				l.WithField("cron", "sftp").Warn("sftp events process already running, skipping...")
			} else {
				l.WithField("cron", "sftp").WithField("error", err).Error("sftp events process failed to execute")
			}
		}
	})

	cleanupCfg := config.Get().System.DiskCleanup
	cleanupInterval := time.Duration(0)
	if cleanupCfg.Volumes.Enabled {
		cleanupInterval = time.Duration(cleanupCfg.Volumes.Interval) * time.Second
	}
	if cleanupCfg.Backups.Enabled {
		bi := time.Duration(cleanupCfg.Backups.Interval) * time.Second
		if cleanupInterval == 0 || bi < cleanupInterval {
			cleanupInterval = bi
		}
	}
	if cleanupInterval > 0 {
		_, _ = s.Tag("disk-cleanup").Every(cleanupInterval).Do(func() {
			l.WithField("cron", "disk-cleanup").Debug("running disk cleanup check")
			if err := diskCleanup.Run(ctx); err != nil {
				if errors.Is(err, ErrCronRunning) {
					l.WithField("cron", "disk-cleanup").Warn("disk cleanup is already running, skipping...")
				} else {
					l.WithField("cron", "disk-cleanup").WithField("error", err).Error("disk cleanup failed to execute")
				}
			}
		})
	}

	killerCfg := config.Get().System.ServerKiller
	if killerCfg.Enabled {
		_, _ = s.Tag("server-killer").Every(time.Duration(killerCfg.Interval) * time.Second).Do(func() {
			l.WithField("cron", "server-killer").Debug("running server killer check")
			if err := serverKiller.Run(ctx); err != nil {
				if errors.Is(err, ErrCronRunning) {
					l.WithField("cron", "server-killer").Warn("server killer is already running, skipping...")
				} else {
					l.WithField("cron", "server-killer").WithField("error", err).Error("server killer failed to execute")
				}
			}
		})
	}

	memKillerCfg := config.Get().System.MemoryKiller
	if memKillerCfg.Enabled {
		_, _ = s.Tag("memory-killer").Every(time.Duration(memKillerCfg.Interval) * time.Second).Do(func() {
			l.WithField("cron", "memory-killer").Debug("running memory killer check")
			if err := memoryKiller.Run(ctx); err != nil {
				if errors.Is(err, ErrCronRunning) {
					l.WithField("cron", "memory-killer").Warn("memory killer is already running, skipping...")
				} else {
					l.WithField("cron", "memory-killer").WithField("error", err).Error("memory killer failed to execute")
				}
			}
		})
	}

	return s, nil
}
