package cron

import (
	"bytes"
	"emperror.dev/errors"
	"encoding/gob"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/sftp"
	"github.com/pterodactyl/wings/system"
	"github.com/xujiajun/nutsdb"
	"path/filepath"
)

type UserDetail struct {
	UUID string
	IP   string
}

type Users map[UserDetail][]sftp.EventRecord
type Events map[sftp.Event]Users

type sftpEventProcessor struct {
	mu      *system.AtomicBool
	manager *server.Manager
}

// Run executes the cronjob and processes sftp activities into normal activity log entries
// by merging together similar records. This helps to reduce the sheer amount of data that
// gets passed back to the Panel and provides simpler activity logging.
func (sep *sftpEventProcessor) Run() error {
	if !sep.mu.SwapIf(true) {
		return errors.WithStack(ErrCronRunning)
	}
	defer sep.mu.Store(false)

	set, err := sep.Events()
	if err != nil {
		return err
	}

	for s, el := range set {
		events := make(Events)
		// Take all of the events that we've pulled out of the system for every server and then
		// parse them into a more usable format in order to create activity log entries for each
		// user, ip, and server instance.
		for _, e := range el {
			u := UserDetail{UUID: e.User, IP: e.IP}
			if _, ok := events[e.Event]; !ok {
				events[e.Event] = make(Users)
			}
			if _, ok := events[e.Event][u]; !ok {
				events[e.Event][u] = []sftp.EventRecord{}
			}
			events[e.Event][u] = append(events[e.Event][u], e)
		}

		// Now that we have all of the events, go ahead and create a normal activity log entry
		// for each instance grouped by user & IP for easier Panel reporting.
		for k, v := range events {
			for u, records := range v {
				files := make([]interface{}, len(records))
				for i, r := range records {
					if r.Action.Target != "" {
						files[i] = map[string]string{
							"from": filepath.Clean(r.Action.Entity),
							"to":   filepath.Clean(r.Action.Target),
						}
					} else {
						files[i] = filepath.Clean(r.Action.Entity)
					}
				}

				entry := server.Activity{
					Server:   s,
					User:     u.UUID,
					Event:    server.Event("server:sftp." + string(k)),
					Metadata: server.ActivityMeta{"files": files},
					IP:       u.IP,
					// Just assume that the first record in the set is the oldest and the most relevant
					// of the timestamps to use.
					Timestamp: records[0].Timestamp,
				}

				if err := entry.Save(); err != nil {
					return errors.Wrap(err, "cron: failed to save new event for server")
				}

				if err := sep.Cleanup([]byte(s)); err != nil {
					return errors.Wrap(err, "cron: failed to cleanup events")
				}
			}
		}
	}

	return nil
}

// Cleanup runs through all of the events we have currently tracked in the bucket and removes
// them once we've managed to process them and created the associated server activity events.
func (sep *sftpEventProcessor) Cleanup(key []byte) error {
	return database.DB().Update(func(tx *nutsdb.Tx) error {
		s, err := sep.sizeOf(tx, key)
		if err != nil {
			return err
		}
		if s == 0 {
			return nil
		} else if s < sep.limit() {
			for i := 0; i < s; i++ {
				if _, err := tx.LPop(database.SftpActivityBucket, key); err != nil {
					return errors.WithStack(err)
				}
			}
		} else {
			if err := tx.LTrim(database.ServerActivityBucket, key, sep.limit()-1, -1); err != nil {
				return errors.WithStack(err)
			}
		}
		return nil
	})
}

// Events pulls all of the events in the SFTP event bucket and parses them into an iterable
// set allowing Wings to process the events and send them back to the Panel instance.
func (sep *sftpEventProcessor) Events() (map[string][]sftp.EventRecord, error) {
	set := make(map[string][]sftp.EventRecord, len(sep.manager.Keys()))
	err := database.DB().View(func(tx *nutsdb.Tx) error {
		for _, k := range sep.manager.Keys() {
			lim := sep.limit()
			if s, err := sep.sizeOf(tx, []byte(k)); err != nil {
				return err
			} else if s == 0 {
				continue
			} else if s < lim {
				lim = -1
			}
			list, err := tx.LRange(database.SftpActivityBucket, []byte(k), 0, lim)
			if err != nil {
				return errors.WithStack(err)
			}
			set[k] = make([]sftp.EventRecord, len(list))
			for i, l := range list {
				if err := gob.NewDecoder(bytes.NewBuffer(l)).Decode(&set[k][i]); err != nil {
					return errors.WithStack(err)
				}
			}
		}
		return nil
	})

	return set, err
}

// sizeOf is a wrapper around a nutsdb transaction to get the size of a key in the
// bucket while also accounting for some expected error conditions and handling those
// automatically.
func (sep *sftpEventProcessor) sizeOf(tx *nutsdb.Tx, key []byte) (int, error) {
	s, err := tx.LSize(database.SftpActivityBucket, key)
	if err != nil {
		if errors.Is(err, nutsdb.ErrBucket) {
			return 0, nil
		}
		return 0, errors.WithStack(err)
	}
	return s, nil
}

// limit returns the number of records that are processed for each server at
// once. This will then be translated into a variable number of activity log
// events, with the worst case being a single event with "n" associated files.
func (sep *sftpEventProcessor) limit() int {
	return 500
}
