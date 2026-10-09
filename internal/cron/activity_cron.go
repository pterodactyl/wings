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

	if err := pruneActivity(ctx, maxStoredActivity); err != nil {
		return err
	}

	// Keep sending batches until the stored activity has been sent, so that it is
	// sent as quickly as it is created.
	for i := 0; i < maxBatchesPerRun; i++ {
		n, err := ac.sendBatch(ctx)
		if err != nil || n < ac.batchSize() {
			return err
		}
	}
	return nil
}

func (ac *activityCron) batchSize() int {
	if ac.max <= 0 {
		return 100
	}
	return ac.max
}

// sendBatch sends the oldest stored activity to the Panel and returns how many
// rows were read.
func (ac *activityCron) sendBatch(ctx context.Context) (int, error) {
	var activity []models.Activity
	tx := database.Instance().WithContext(ctx).
		Where("event NOT LIKE ?", "server:sftp.%").
		Order("id ASC").
		Limit(ac.batchSize()).
		Find(&activity)
	if tx.Error != nil {
		return 0, errors.WithStack(tx.Error)
	}
	if len(activity) == 0 {
		return 0, nil
	}

	// ids of activity to delete without sending it.
	var invalid []int
	items := make([]batchItem, 0, len(activity))
	for _, v := range activity {
		// Delete any activity that has an invalid IP address. This is a fix for
		// a bug that truncated the last octet of an IPv6 address in the database.
		if ip := net.ParseIP(v.IP); ip == nil {
			invalid = append(invalid, v.ID)
			continue
		}
		items = append(items, batchItem{activity: v, ids: []int{v.ID}})
	}

	done, err := deliver(ctx, ac.manager.Client(), items)
	if derr := deleteActivity(ctx, append(invalid, done...)); derr != nil {
		return 0, derr
	}
	if err != nil {
		return 0, errors.WrapIf(err, "cron: failed to send activity events to Panel")
	}
	return len(activity), nil
}
