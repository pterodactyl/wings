package cron

import (
	"context"
	"reflect"

	"emperror.dev/errors"

	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

type sftpCron struct {
	mu      *system.AtomicBool
	manager *server.Manager
	max     int
}

type mapKey struct {
	User      string
	Server    string
	IP        string
	Event     models.Event
	Timestamp string
}

type eventMap struct {
	max int
	ids []int
	m   map[mapKey]*models.Activity
}

// Run executes the SFTP reconciliation cron. This job will pull all of the SFTP specific events
// and merge them together across user, server, ip, and event. This allows a SFTP event that deletes
// tens or hundreds of files to be tracked as a single "deletion" event so long as they all occur
// within the same one minute period of time (starting at the first timestamp for the group). Without
// this we'd end up flooding the Panel event log with excessive data that is of no use to end users.
func (sc *sftpCron) Run(ctx context.Context) error {
	if !sc.mu.SwapIf(true) {
		return errors.WithStack(ErrCronRunning)
	}
	defer sc.mu.Store(false)

	var o int
	activity, err := sc.fetchRecords(ctx, o)
	if err != nil {
		return err
	}
	o += len(activity)

	events := &eventMap{
		m:   map[mapKey]*models.Activity{},
		ids: []int{},
		max: sc.max,
	}

	for {
		if len(activity) == 0 {
			break
		}
		slen := len(events.ids)
		for _, a := range activity {
			events.Push(a)
		}
		if len(events.ids) > slen {
			// Execute the query again, we found some events so we want to continue
			// with this. Start at the next offset.
			activity, err = sc.fetchRecords(ctx, o)
			if err != nil {
				return errors.WithStack(err)
			}
			o += len(activity)
		} else {
			break
		}
	}

	if len(events.m) == 0 {
		return nil
	}
	if err := sc.manager.Client().SendActivityLogs(ctx, events.Elements()); err != nil {
		return errors.Wrap(err, "failed to send sftp activity logs to Panel")
	}
	if tx := database.Instance().Where("id IN ?", events.ids).Delete(&models.Activity{}); tx.Error != nil {
		return errors.WithStack(tx.Error)
	}
	return nil
}

// fetchRecords returns a group of activity events starting at the given offset. This is used
// since we might need to make multiple database queries to select enough events to properly
// fill up our request to the given maximum. This is due to the fact that this cron merges any
// activity that line up across user, server, ip, and event into a single activity record when
// sending the data to the Panel.
func (sc *sftpCron) fetchRecords(ctx context.Context, offset int) (activity []models.Activity, err error) {
	tx := database.Instance().WithContext(ctx).
		Where("event LIKE ?", "server:sftp.%").
		Order("event DESC").
		Offset(offset).
		Limit(sc.max).
		Find(&activity)
	if tx.Error != nil {
		err = errors.WithStack(tx.Error)
	}
	return
}

// Push adds an activity to the event mapping, or de-duplicates it and merges the files metadata
// into the existing entity that exists.
func (em *eventMap) Push(a models.Activity) {
	m := em.forActivity(a)
	// If no activity entity is returned we've hit the cap for the number of events to
	// send along to the Panel. Just skip over this record and we'll account for it in
	// the next iteration.
	if m == nil {
		return
	}
	em.ids = append(em.ids, a.ID)
	// Always reduce this to the first timestamp that was recorded for the set
	// of events, and not
	if a.Timestamp.Before(m.Timestamp) {
		m.Timestamp = a.Timestamp
	}
	list := m.Metadata["files"].([]interface{})
	if s, ok := a.Metadata["files"]; ok {
		v := reflect.ValueOf(s)
		if v.Kind() != reflect.Slice || v.IsNil() {
			return
		}
		for i := 0; i < v.Len(); i++ {
			list = append(list, v.Index(i).Interface())
		}
		// You must set it again at the end of the process, otherwise you've only updated the file
		// slice in this one loop since it isn't passed by reference. This is just shorter than having
		// to explicitly keep casting it to the slice.
		m.Metadata["files"] = list
	}
}

// Elements returns the finalized activity models.
func (em *eventMap) Elements() (out []models.Activity) {
	for _, v := range em.m {
		out = append(out, *v)
	}
	return
}

// forActivity returns an event entity from our map which allows existing matches to be
// updated with additional files.
func (em *eventMap) forActivity(a models.Activity) *models.Activity {
	key := mapKey{
		User:   a.User.String,
		Server: a.Server,
		IP:     a.IP,
		Event:  a.Event,
		// We group by the minute, don't care about the seconds for this logic.
		Timestamp: a.Timestamp.Format("2006-01-02_15:04"),
	}
	if v, ok := em.m[key]; ok {
		return v
	}
	// Cap the size of the events map at the defined maximum events to send to the Panel. Just
	// return nil and let the caller handle it.
	if len(em.m) >= em.max {
		return nil
	}
	// Doesn't exist in our map yet, create a copy of the activity passed into this
	// function and then assign it into the map with an empty metadata value.
	v := a
	v.Metadata = models.ActivityMeta{
		"files": make([]interface{}, 0),
	}
	em.m[key] = &v
	return &v
}
