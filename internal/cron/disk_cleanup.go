package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/google/uuid"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

type diskCleanupCron struct {
	mu              *system.AtomicBool
	manager         *server.Manager
	lastVolumesRun  time.Time
	lastBackupsRun  time.Time
}

// cleanupCandidate represents a file or directory that can be deleted to free
// up disk space.
type cleanupCandidate struct {
	path  string
	size  int64
	mtime time.Time
}

func newDiskCleanupCron(m *server.Manager) *diskCleanupCron {
	return &diskCleanupCron{
		mu:      system.NewAtomicBool(false),
		manager: m,
	}
}

// Run executes the disk cleanup cron. It evaluates both volumes and backups
// cleanup configs independently, respecting each one's interval.
func (dc *diskCleanupCron) Run(_ context.Context) error {
	if !dc.mu.SwapIf(true) {
		return errors.WithStack(ErrCronRunning)
	}
	defer dc.mu.Store(false)

	cfg := config.Get().System.DiskCleanup
	now := time.Now()

	tgCfg := config.Get().System.TelegramNotifications.AsSystemConfig()

	if cfg.Volumes.Enabled {
		if dc.lastVolumesRun.IsZero() || now.Sub(dc.lastVolumesRun) >= time.Duration(cfg.Volumes.Interval)*time.Second {
			if err := dc.cleanupVolumes(cfg.Volumes, tgCfg); err != nil {
				log.WithError(err).Error("disk cleanup: failed to cleanup volumes")
			}
			dc.lastVolumesRun = now
		}
	}

	if cfg.Backups.Enabled {
		if dc.lastBackupsRun.IsZero() || now.Sub(dc.lastBackupsRun) >= time.Duration(cfg.Backups.Interval)*time.Second {
			if err := dc.cleanupBackups(cfg.Backups, tgCfg); err != nil {
				log.WithError(err).Error("disk cleanup: failed to cleanup backups")
			}
			dc.lastBackupsRun = now
		}
	}

	return nil
}

// cleanupVolumes removes the oldest stopped server directories under the
// configured data directory until disk usage drops to the configured target.
func (dc *diskCleanupCron) cleanupVolumes(cfg config.DiskCleanupConfig, tgCfg system.TelegramConfig) error {
	if !cfg.IsValid() {
		log.Warn("disk cleanup: volumes configuration is invalid, skipping")
		return nil
	}

	path := config.Get().System.Data
	used, total, _, err := system.DiskUsagePercent(path)
	if err != nil {
		return errors.Wrap(err, "disk cleanup: failed to get volumes disk usage")
	}

	if used <= float64(cfg.Threshold) {
		return nil
	}

	log.WithFields(log.Fields{
		"path":      path,
		"used":      used,
		"threshold": cfg.Threshold,
	}).Info("disk cleanup: volumes usage above threshold, starting cleanup")

	entries, err := os.ReadDir(path)
	if err != nil {
		return errors.Wrap(err, "disk cleanup: failed to read volumes directory")
	}

	var candidates []cleanupCandidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		if _, err := uuid.Parse(name); err != nil {
			continue
		}

		// Do not delete servers that are currently running or performing an
		// operation that could be writing data.
		if s, ok := dc.manager.Get(name); ok {
			state := s.Environment.State()
			if state == environment.ProcessRunningState || state == environment.ProcessStartingState {
				continue
			}
			if s.IsInstalling() || s.IsRestoring() || s.IsTransferring() {
				continue
			}
		}

		dirPath := filepath.Join(path, name)
		size, err := dc.dirSize(dirPath)
		if err != nil {
			log.WithError(err).WithField("path", dirPath).Warn("disk cleanup: failed to calculate directory size")
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		candidates = append(candidates, cleanupCandidate{
			path:  dirPath,
			size:  size,
			mtime: info.ModTime(),
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mtime.Before(candidates[j].mtime)
	})

	neededBytes := int64((used - float64(cfg.Target)) / 100 * float64(total))
	if neededBytes <= 0 {
		return nil
	}

	var freed int64
	for _, c := range candidates {
		if freed >= neededBytes {
			break
		}

		if err := os.RemoveAll(c.path); err != nil {
			log.WithError(err).WithField("path", c.path).Error("disk cleanup: failed to remove server directory")
			continue
		}

		freed += c.size
		log.WithFields(log.Fields{
			"path": c.path,
			"size": c.size,
		}).Info("disk cleanup: removed server directory")
	}

	usedAfter, _, _, err := system.DiskUsagePercent(path)
	if err != nil {
		return errors.Wrap(err, "disk cleanup: failed to get volumes disk usage after cleanup")
	}

	log.WithFields(log.Fields{
		"path":        path,
		"used_before": used,
		"used_after":  usedAfter,
		"freed":       freed,
	}).Info("disk cleanup: volumes cleanup complete")

	if freed > 0 {
		msg := fmt.Sprintf(
			"🧹 *Disk Cleanup: Volumes*\nPath: `%s`\nUsed before: %.1f%%\nUsed after: %.1f%%\nFreed: %s",
			path, used, usedAfter, humanBytes(freed),
		)
		system.SendTelegramNotificationAsync(tgCfg, msg)
	}

	return nil
}

// cleanupBackups removes the oldest backup archive files under the configured
// backup directory until disk usage drops to the configured target.
func (dc *diskCleanupCron) cleanupBackups(cfg config.DiskCleanupConfig, tgCfg system.TelegramConfig) error {
	if !cfg.IsValid() {
		log.Warn("disk cleanup: backups configuration is invalid, skipping")
		return nil
	}

	path := config.Get().System.BackupDirectory
	used, total, _, err := system.DiskUsagePercent(path)
	if err != nil {
		return errors.Wrap(err, "disk cleanup: failed to get backups disk usage")
	}

	if used <= float64(cfg.Threshold) {
		return nil
	}

	log.WithFields(log.Fields{
		"path":      path,
		"used":      used,
		"threshold": cfg.Threshold,
	}).Info("disk cleanup: backups usage above threshold, starting cleanup")

	var candidates []cleanupCandidate
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		candidates = append(candidates, cleanupCandidate{
			path:  p,
			size:  info.Size(),
			mtime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return errors.Wrap(err, "disk cleanup: failed to walk backups directory")
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mtime.Before(candidates[j].mtime)
	})

	neededBytes := int64((used - float64(cfg.Target)) / 100 * float64(total))
	if neededBytes <= 0 {
		return nil
	}

	var freed int64
	for _, c := range candidates {
		if freed >= neededBytes {
			break
		}

		if err := os.Remove(c.path); err != nil {
			log.WithError(err).WithField("path", c.path).Error("disk cleanup: failed to remove backup file")
			continue
		}

		freed += c.size
		log.WithFields(log.Fields{
			"path": c.path,
			"size": c.size,
		}).Info("disk cleanup: removed backup file")
	}

	usedAfter, _, _, err := system.DiskUsagePercent(path)
	if err != nil {
		return errors.Wrap(err, "disk cleanup: failed to get backups disk usage after cleanup")
	}

	log.WithFields(log.Fields{
		"path":        path,
		"used_before": used,
		"used_after":  usedAfter,
		"freed":       freed,
	}).Info("disk cleanup: backups cleanup complete")

	if freed > 0 {
		msg := fmt.Sprintf(
			"🧹 *Disk Cleanup: Backups*\nPath: `%s`\nUsed before: %.1f%%\nUsed after: %.1f%%\nFreed: %s",
			path, used, usedAfter, humanBytes(freed),
		)
		system.SendTelegramNotificationAsync(tgCfg, msg)
	}

	return nil
}

// humanBytes converts bytes into a human readable string.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// dirSize returns the total size of all regular files under the given path.
func (dc *diskCleanupCron) dirSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		size += info.Size()
		return nil
	})
	return size, err
}
