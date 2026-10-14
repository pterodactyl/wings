package cron

import (
	"context"
	"net/http"

	"emperror.dev/errors"
	"github.com/apex/log"

	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/remote"
)

const (
	// maxBatchesPerRun is the most batches of activity sent to the Panel in one
	// run, so that a large backlog is sent over several runs.
	maxBatchesPerRun = 50
	// maxStoredActivity is the most activity kept while it waits to be sent. Once
	// there is more, the oldest is deleted.
	maxStoredActivity = 100_000
)

// batchItem is an activity to send to the Panel along with the IDs of the
// stored rows it was made from.
type batchItem struct {
	activity models.Activity
	ids      []int
}

// deliver sends the items to the Panel and returns the IDs of the rows that no
// longer need to be kept. If the Panel refuses a batch as invalid, it is split
// to find the items it refuses, which are dropped so that they do not stop the
// rest from ever being sent.
func deliver(ctx context.Context, client remote.Client, items []batchItem) ([]int, error) {
	if len(items) == 0 {
		return nil, nil
	}
	activity := make([]models.Activity, len(items))
	for i, item := range items {
		activity[i] = item.activity
	}

	err := client.SendActivityLogs(ctx, activity)
	if err == nil {
		var ids []int
		for _, item := range items {
			ids = append(ids, item.ids...)
		}
		return ids, nil
	}
	if !isRefused(err) {
		return nil, err
	}
	if len(items) == 1 {
		log.WithField("subsystem", "cron").
			WithField("event", items[0].activity.Event).
			WithField("server", items[0].activity.Server).
			WithField("error", err).
			Warn("dropping activity event refused by the Panel")
		return items[0].ids, nil
	}

	mid := len(items) / 2
	done, err := deliver(ctx, client, items[:mid])
	if err != nil {
		return done, err
	}
	rest, err := deliver(ctx, client, items[mid:])
	return append(done, rest...), err
}

// isRefused reports whether the Panel refused a request because of its contents,
// in which case sending it again cannot succeed.
func isRefused(err error) bool {
	rerr := remote.AsRequestError(err)
	if rerr == nil {
		return false
	}
	switch rerr.StatusCode() {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return true
	default:
		return false
	}
}

// deleteActivity deletes the stored activity with the given IDs.
func deleteActivity(ctx context.Context, ids []int) error {
	// SQLite limits how many parameters a single query can have, so delete the
	// rows in chunks.
	for start := 0; start < len(ids); start += 32000 {
		end := min(start+32000, len(ids))
		tx := database.Instance().WithContext(ctx).Where("id IN ?", ids[start:end]).Delete(&models.Activity{})
		if tx.Error != nil {
			return errors.WithStack(tx.Error)
		}
	}
	return nil
}

// pruneActivity deletes the oldest stored activity once there is more than max.
func pruneActivity(ctx context.Context, max int64) error {
	db := database.Instance().WithContext(ctx)
	var count int64
	if tx := db.Model(&models.Activity{}).Count(&count); tx.Error != nil {
		return errors.WithStack(tx.Error)
	}
	if count <= max {
		return nil
	}
	excess := count - max
	oldest := db.Model(&models.Activity{}).Select("id").Order("id ASC").Limit(int(excess))
	if tx := db.Where("id IN (?)", oldest).Delete(&models.Activity{}); tx.Error != nil {
		return errors.WithStack(tx.Error)
	}
	log.WithField("subsystem", "cron").WithField("deleted", excess).
		Warn("deleted the oldest stored activity events because too many are waiting to be sent")
	return nil
}
