package cron

import (
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/go-co-op/gocron"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
	"time"
)

var o system.AtomicBool

// Scheduler configures the internal cronjob system for Wings and returns the scheduler
// instance to the caller. This should only be called once per application lifecycle, additional
// calls will result in an error being returned.
func Scheduler(m *server.Manager) (*gocron.Scheduler, error) {
	if o.Load() {
		return nil, errors.New("cron: cannot call scheduler more than once in application lifecycle")
	}
	o.Store(true)
	l, err := time.LoadLocation(config.Get().System.Timezone)
	if err != nil {
		return nil, errors.Wrap(err, "cron: failed to parse configured system timezone")
	}

	s := gocron.NewScheduler(l)
	_, _ = s.Tag("activity").Every(int(config.Get().System.ActivitySendInterval)).Seconds().Do(func() {
		if err := processActivityLogs(m, config.Get().System.ActivitySendCount); err != nil {
			log.WithField("error", err).Error("cron: failed to process activity events")
		}
	})

	return s, nil
}
