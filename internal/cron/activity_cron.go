package cron

import (
	"context"
	"net"

	"emperror.dev/errors"

	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

type activityCron struct {
	mu      *system.AtomicBool
	manager *server.Manager
	max     int
}

// Run executes the cronjob and ensures we fetch and send all the stored activity to the
// Panel instance. Once activity is sent it is deleted from the local database instance. Any
// SFTP specific events are not handled in this cron, they're handled separately to account
// for de-duplication and event merging.
func (ac *activityCron) Run(ctx context.Context) error {
	// Don't execute this cron if there is currently one running. Once this task is completed
	// go ahead and mark it as no longer running.
	if !ac.mu.SwapIf(true) {
		return errors.WithStack(ErrCronRunning)
	}
	defer ac.mu.Store(false)

	var activity []models.Activity
	tx := database.Instance().WithContext(ctx).
		Where("event NOT LIKE ?", "server:sftp.%").
		Limit(ac.max).
		Find(&activity)
	if tx.Error != nil {
		return errors.WithStack(tx.Error)
	}
	if len(activity) == 0 {
		return nil
	}

	// ids to delete from the database.
	ids := make([]int, 0, len(activity))
	// activities to send to the panel.
	activities := make([]models.Activity, 0, len(activity))
	for _, v := range activity {
		// Delete any activity that has an invalid IP address. This is a fix for
		// a bug that truncated the last octet of an IPv6 address in the database.
		if ip := net.ParseIP(v.IP); ip == nil {
			ids = append(ids, v.ID)
			continue
		}
		activities = append(activities, v)
	}

	// Delete any invalid activies
	if len(ids) > 0 {
		tx = database.Instance().WithContext(ctx).Where("id IN ?", ids).Delete(&models.Activity{})
		if tx.Error != nil {
			return errors.WithStack(tx.Error)
		}
	}

	if len(activities) == 0 {
		return nil
	}

	if err := ac.manager.Client().SendActivityLogs(ctx, activities); err != nil {
		return errors.WrapIf(err, "cron: failed to send activity events to Panel")
	}

	ids = make([]int, len(activities))
	for i, v := range activities {
		ids[i] = v.ID
	}

	// SQLite has a limitation of how many parameters we can specify in a single
	// query, so we need to delete the activies in chunks of 32,000 instead of
	// all at once.
	i := 0
	idsLen := len(ids)
	for i < idsLen {
		start := i
		end := min(i+32000, idsLen)
		batchSize := end - start

		tx = database.Instance().WithContext(ctx).Where("id IN ?", ids[start:end]).Delete(&models.Activity{})
		if tx.Error != nil {
			return errors.WithStack(tx.Error)
		}

		i += batchSize
	}

	return nil
}
