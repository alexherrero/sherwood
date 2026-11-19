package main

import (
	"log/slog"

	"github.com/alexherrero/sherwood/internal/backup"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/store"
)

// registerBackup registers the nightly backup at 02:00 US Eastern, every day.
// With no backup folder in the settings file it is still registered, so each
// night's run fails and you are told, rather than backups quietly not
// happening.
func registerBackup(sched *scheduler.Scheduler, cfg config.Backup, db *store.Store, logger *slog.Logger) error {
	if cfg.Dir == "" {
		logger.Warn("no backup folder is set, so every nightly backup will fail until backup.dir is added to the settings file")
	}
	b, err := backup.New(backup.Config{Store: db, Dir: cfg.Dir, Keep: cfg.Keep, Logger: logger})
	if err != nil {
		return err
	}
	return sched.Register(b.Job())
}
