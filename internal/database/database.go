package database

import (
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/config"
	"github.com/xujiajun/nutsdb"
	"path/filepath"
	"sync"
)

var db *nutsdb.DB
var syncer sync.Once

const (
	ServerActivityBucket = "server_activity"
	SftpActivityBucket   = "sftp_activity"
)

func initialize() error {
	opt := nutsdb.DefaultOptions
	opt.Dir = filepath.Join(config.Get().System.RootDirectory, "db")

	instance, err := nutsdb.Open(opt)
	if err != nil {
		return errors.WithStack(err)
	}
	db = instance
	return nil
}

func DB() *nutsdb.DB {
	syncer.Do(func() {
		if err := initialize(); err != nil {
			log.WithField("error", err).Fatal("database: failed to initialize instance, this is an unrecoverable error")
		}
	})
	return db
}
