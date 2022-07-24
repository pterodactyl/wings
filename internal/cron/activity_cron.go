package cron

import (
	"bytes"
	"context"
	"database/sql"
	"emperror.dev/errors"
	"encoding/gob"
	"github.com/pterodactyl/wings/internal/sqlite"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
	"strings"
)

type activityCron struct {
	mu      *system.AtomicBool
	manager *server.Manager
	max     int64
}

const queryRegularActivity = `
SELECT id, event, user_uuid, server_uuid, metadata, ip, timestamp FROM activity_logs
	WHERE event NOT LIKE 'server:sftp.%'
	ORDER BY timestamp
	LIMIT ?
`

type QueriedActivity struct {
	id int
	b  []byte
	server.Activity
}

// Parse parses the internal query results into the QueriedActivity type and then properly
// sets the Metadata onto it. This also sets the ID that was returned to ensure we're able
// to then delete all of the matching rows in the database after we're done.
func (qa *QueriedActivity) Parse(r *sql.Rows) error {
	if err := r.Scan(&qa.id, &qa.Event, &qa.User, &qa.Server, &qa.b, &qa.IP, &qa.Timestamp); err != nil {
		return errors.Wrap(err, "cron: failed to parse activity log")
	}
	if err := gob.NewDecoder(bytes.NewBuffer(qa.b)).Decode(&qa.Metadata); err != nil {
		return errors.WithStack(err)
	}
	return nil
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

	rows, err := sqlite.Instance().QueryContext(ctx, queryRegularActivity, ac.max)
	if err != nil {
		return errors.Wrap(err, "cron: failed to query activity logs")
	}
	defer rows.Close()

	var logs []server.Activity
	var ids []int
	for rows.Next() {
		var qa QueriedActivity
		if err := qa.Parse(rows); err != nil {
			return err
		}
		ids = append(ids, qa.id)
		logs = append(logs, qa.Activity)
	}

	if err := rows.Err(); err != nil {
		return errors.WithStack(err)
	}
	if len(logs) == 0 {
		return nil
	}
	if err := ac.manager.Client().SendActivityLogs(ctx, logs); err != nil {
		return errors.WrapIf(err, "cron: failed to send activity events to Panel")
	}

	if tx, err := sqlite.Instance().Begin(); err != nil {
		return err
	} else {
		t := make([]string, len(ids))
		params := make([]interface{}, len(ids))
		for i := 0; i < len(ids); i++ {
			t[i] = "?"
			params[i] = ids[i]
		}
		q := strings.Join(t, ",")
		_, err := tx.Exec(`DELETE FROM activity_logs WHERE id IN(`+q+`)`, params...)
		if err != nil {
			return errors.Combine(errors.WithStack(err), tx.Rollback())
		}
		return errors.WithStack(tx.Commit())
	}
}
