package cron

import (
	"context"
	"emperror.dev/errors"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
	"github.com/xujiajun/nutsdb"
)

var processing system.AtomicBool

func processActivityLogs(m *server.Manager) error {
	// Don't execute this cron if there is currently one running. Once this task is completed
	// go ahead and mark it as no longer running.
	if !processing.SwapIf(true) {
		return nil
	}
	defer processing.Store(false)

	var b [][]byte
	err := database.DB().View(func(tx *nutsdb.Tx) error {
		// Grab the oldest 100 activity events that have been logged and send them back to the
		// Panel for processing. Once completed, delete those events from the database and then
		// release the lock on this process.
		list, err := tx.LRange(database.ServerActivityBucket, []byte("events"), 0, 1)
		if err != nil {
			// This error is returned when the bucket doesn't exist, which is likely on the
			// first invocations of Wings since we haven't yet logged any data. There is nothing
			// that needs to be done if this error occurs.
			if errors.Is(err, nutsdb.ErrBucket) {
				return nil
			}
			return errors.WithStackIf(err)
		}
		b = list
		return nil
	})

	// If there is an error, return it. If there is no data to send to the Panel don't waste
	// an API call, just return here. WithStackIf will return "nil" when the value provided to
	// it is also nil.
	if err != nil || len(b) == 0 {
		return errors.WithStackIf(err)
	}

	if err := m.Client().SendActivityLogs(context.Background(), b); err != nil {
		return errors.WrapIf(err, "cron: failed to send activity events to Panel")
	}

	return database.DB().Update(func(tx *nutsdb.Tx) error {
		return tx.LTrim(database.ServerActivityBucket, []byte("events"), 2, -1)
	})
}
