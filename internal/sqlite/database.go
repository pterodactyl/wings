package sqlite

import (
	"context"
	"database/sql"
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/system"
	_ "modernc.org/sqlite"
	"path/filepath"
)

var o system.AtomicBool
var db *sql.DB

const schema = `
CREATE TABLE IF NOT EXISTS "activity_logs" (
	"id" integer,
	"event" varchar NOT NULL,
	"user_uuid" varchar,
	"server_uuid" varchar NOT NULL,
	"metadata" blob,
	"ip" varchar,
	"timestamp" integer NOT NULL,
	PRIMARY KEY (id)
);

-- Add an index otherwise we're gonna end up with performance issues over time especially
-- on huge Wings instances where we'll have a large number of activity logs to parse through.
CREATE INDEX IF NOT EXISTS idx_event ON activity_logs(event);
`

func Initialize(ctx context.Context) error {
	if !o.SwapIf(true) {
		panic("database: attempt to initialize more than once during application lifecycle")
	}
	p := filepath.Join(config.Get().System.RootDirectory, "wings.db")
	log.WithField("subsystem", "sqlite").WithField("path", p).Info("initializing local database")
	database, err := sql.Open("sqlite", p)
	if err != nil {
		return errors.Wrap(err, "database: could not open database file")
	}
	db = database
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return errors.Wrap(err, "database: failed to initialize base schema")
	}
	return nil
}

func Instance() *sql.DB {
	if db == nil {
		panic("database: attempt to access instance before initialized")
	}
	return db
}
