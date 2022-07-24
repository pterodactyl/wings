package database

import (
	"emperror.dev/errors"
	"github.com/glebarez/sqlite"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/system"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"path/filepath"
	"time"
)

var o system.AtomicBool
var db *gorm.DB

// Initialize configures the local SQLite database for Wings and ensures that the models have
// been fully migrated.
func Initialize() error {
	if !o.SwapIf(true) {
		panic("database: attempt to initialize more than once during application lifecycle")
	}
	p := filepath.Join(config.Get().System.RootDirectory, "wings.db")
	instance, err := gorm.Open(sqlite.Open(p), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return errors.Wrap(err, "database: could not open database file")
	}
	db = instance
	if sql, err := db.DB(); err != nil {
		return errors.WithStack(err)
	} else {
		sql.SetMaxOpenConns(1)
		sql.SetConnMaxLifetime(time.Hour)
	}
	if err := db.AutoMigrate(&models.Activity{}); err != nil {
		return errors.WithStack(err)
	}
	return nil
}

// Instance returns the gorm database instance that was configured when the application was
// booted.
func Instance() *gorm.DB {
	if db == nil {
		panic("database: attempt to access instance before initialized")
	}
	return db
}
