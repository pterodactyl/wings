package cron

import (
	"context"

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

// Run executes the cronjob and ensures we fetch and send all of the stored activity to the
// Panel instance. Once activity is sent it is deleted from the local database instance. Any
// SFTP specific events are not handled in this cron, they're handled seperately to account
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

	if err := ac.manager.Client().SendActivityLogs(ctx, activity); err != nil {
		return errors.WrapIf(err, "cron: failed to send activity events to Panel")
	}

	var ids []int
	for _, v := range activity {
		ids = append(ids, v.ID)
	}

	tx = database.Instance().WithContext(ctx).Where("id IN ?", ids).Delete(&models.Activity{})
	if tx.Error != nil {
		return errors.WithStack(tx.Error)
	}
	return nil
}
