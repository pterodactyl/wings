package cron

import (
	"context"
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/goccy/go-json"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
	"github.com/xujiajun/nutsdb"
)

var key = []byte("events")
var processing system.AtomicBool

func processActivityLogs(m *server.Manager, c int64) error {
	// Don't execute this cron if there is currently one running. Once this task is completed
	// go ahead and mark it as no longer running.
	if !processing.SwapIf(true) {
		log.WithField("subsystem", "cron").Warn("cron: process overlap detected, skipping this run")
		return nil
	}
	defer processing.Store(false)

	var list [][]byte
	err := database.DB().View(func(tx *nutsdb.Tx) error {
		// Grab the oldest 100 activity events that have been logged and send them back to the
		// Panel for processing. Once completed, delete those events from the database and then
		// release the lock on this process.
		end := int(c)
		if s, err := tx.LSize(database.ServerActivityBucket, key); err != nil {
			return errors.WithStackIf(err)
		} else if s < end || s == 0 {
			if s == 0 {
				return nil
			}
			end = s
		}
		l, err := tx.LRange(database.ServerActivityBucket, key, 0, end)
		if err != nil {
			// This error is returned when the bucket doesn't exist, which is likely on the
			// first invocations of Wings since we haven't yet logged any data. There is nothing
			// that needs to be done if this error occurs.
			if errors.Is(err, nutsdb.ErrBucket) {
				return nil
			}
			return errors.WithStackIf(err)
		}
		list = l
		return nil
	})

	if err != nil || len(list) == 0 {
		return errors.WithStackIf(err)
	}

	var processed []json.RawMessage
	for _, l := range list {
		var v json.RawMessage
		if err := json.Unmarshal(l, &v); err != nil {
			log.WithField("error", errors.WithStack(err)).Warn("failed to parse activity event json, skipping entry")
			continue
		}
		processed = append(processed, v)
	}

	if err := m.Client().SendActivityLogs(context.Background(), processed); err != nil {
		return errors.WrapIf(err, "cron: failed to send activity events to Panel")
	}

	return database.DB().Update(func(tx *nutsdb.Tx) error {
		if m, err := tx.LSize(database.ServerActivityBucket, key); err != nil {
			return errors.WithStack(err)
		} else if m > len(list) {
			// As long as there are more elements than we have in the length of our list
			// we can just use the existing `LTrim` functionality of nutsdb. This will remove
			// all of the values we've already pulled and sent to the API.
			return errors.WithStack(tx.LTrim(database.ServerActivityBucket, key, len(list), -1))
		} else {
			i := 0
			// This is the only way I can figure out to actually empty the items out of the list
			// because you cannot use `LTrim` (or I cannot for the life of me figure out how) to
			// trim the slice down to 0 items without it triggering an internal logic error. Perhaps
			// in a future release they'll have a function to do this (based on my skimming of issues
			// on GitHub that I cannot read due to translation barriers).
			for {
				if i >= m {
					break
				}
				if _, err := tx.LPop(database.ServerActivityBucket, key); err != nil {
					return errors.WithStack(err)
				}
				i++
			}
		}
		return nil
	})
}
