package cron

import (
	"bytes"
	"context"
	"emperror.dev/errors"
	"encoding/gob"
	"github.com/pterodactyl/wings/internal/sqlite"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
	"time"
)

const querySftpActivity = `
SELECT
	event,
	user_uuid,
	server_uuid,
	ip,
	GROUP_CONCAT(metadata, '::') AS metadata,
	MIN(timestamp) AS first_timestamp
FROM activity_logs
WHERE event LIKE 'server:sftp.%'
GROUP BY event, STRFTIME('%Y-%m-%d %H:%M:00', DATETIME(timestamp, 'unixepoch', 'utc')), user_uuid, server_uuid, ip
LIMIT ?
`

type sftpActivityGroup struct {
	Event     server.Event
	User      string
	Server    string
	IP        string
	Metadata  []byte
	Timestamp int64
}

// Activity takes the struct and converts it into a single activity entity to
// process and send back over to the Panel.
func (g *sftpActivityGroup) Activity() (server.Activity, error) {
	m, err := g.processMetadata()
	if err != nil {
		return server.Activity{}, err
	}
	t := time.Unix(g.Timestamp, 0)
	a := server.Activity{
		User:      g.User,
		Server:    g.Server,
		Event:     g.Event,
		Metadata:  m,
		IP:        g.IP,
		Timestamp: t.UTC(),
	}
	return a, nil
}

// processMetadata takes all of the concatenated metadata returned by the SQL
// query and then processes it all into individual entity records before then
// merging them into a final, single metadata, object.
func (g *sftpActivityGroup) processMetadata() (server.ActivityMeta, error) {
	b := bytes.Split(g.Metadata, []byte("::"))
	if len(b) == 0 {
		return server.ActivityMeta{}, nil
	}
	entities := make([]server.ActivityMeta, len(b))
	for i, v := range b {
		if len(v) == 0 {
			continue
		}
		if err := gob.NewDecoder(bytes.NewBuffer(v)).Decode(&entities[i]); err != nil {
			return nil, errors.Wrap(err, "could not decode metadata bytes")
		}
	}
	var files []interface{}
	// Iterate over every entity that we've gotten back from the database's metadata fields
	// and merge them all into a single entity by checking what the data type returned is and
	// going from there.
	//
	// We only store a slice of strings, or a string/string map value in the database for SFTP
	// actions, hence the case statement.
	for _, e := range entities {
		if e == nil {
			continue
		}
		if f, ok := e["files"]; ok {
			var a []interface{}
			switch f.(type) {
			case []string:
				for _, v := range f.([]string) {
					a = append(a, v)
				}
			case map[string]string:
				a = append(a, f)
			}
			files = append(files, a)
		}
	}
	return server.ActivityMeta{"files": files}, nil
}

type sftpActivityCron struct {
	mu      *system.AtomicBool
	manager *server.Manager
	max     int64
}

// Run executes the cronjob and finds all associated SFTP events, bundles them up so
// that multiple events in the same timespan are recorded as a single event, and then
// cleans up the database.
func (sac *sftpActivityCron) Run(ctx context.Context) error {
	if !sac.mu.SwapIf(true) {
		return errors.WithStack(ErrCronRunning)
	}
	defer sac.mu.Store(false)

	rows, err := sqlite.Instance().QueryContext(ctx, querySftpActivity, sac.max)
	if err != nil {
		return errors.Wrap(err, "cron: failed to query sftp activity")
	}
	defer rows.Close()

	if err := rows.Err(); err != nil {
		return errors.WithStack(err)
	}

	var out []server.Activity
	for rows.Next() {
		v := sftpActivityGroup{}
		if err := rows.Scan(&v.Event, &v.User, &v.Server, &v.IP, &v.Metadata, &v.Timestamp); err != nil {
			return errors.Wrap(err, "failed to scan row")
		}
		if a, err := v.Activity(); err != nil {
			return errors.Wrap(err, "could not parse data into activity type")
		} else {
			out = append(out, a)
		}
	}

	if len(out) == 0 {
		return nil
	}
	if err := sac.manager.Client().SendActivityLogs(ctx, out); err != nil {
		return errors.Wrap(err, "could not send activity logs to Panel")
	}
	return nil
}
