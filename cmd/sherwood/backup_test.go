package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/store"
)

// backupRig opens a database and a scheduler on a Sunday at 02:00, with the
// nightly backup registered from cfg.
func backupRig(t *testing.T, cfg config.Backup) (*scheduler.Scheduler, *notifytest.Fake, *loggingtest.Output, *store.Store) {
	t.Helper()
	now := time.Date(2026, 11, 22, 2, 0, 0, 0, clock.Eastern)
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logger, out := loggingtest.New(t)
	alerts := &notifytest.Fake{}
	sched, err := scheduler.New(scheduler.Config{Store: db, Clock: clock.NewFake(now), Calendar: clock.NYSE(), Logger: logger, Alerts: alerts})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerBackup(sched, cfg, db, logger); err != nil {
		t.Fatal(err)
	}
	return sched, alerts, out, db
}

func TestNightlyBackupRunsOnASunday(t *testing.T) {
	dir := t.TempDir()
	sched, alerts, out, _ := backupRig(t, config.Backup{Dir: dir, Keep: 7})
	if err := sched.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sherwood-2026-11-22.db")); err != nil {
		t.Fatalf("no copy for Sunday: %v", err)
	}
	if len(alerts.Sent()) != 0 {
		t.Errorf("alerts = %v, want none", alerts.Sent())
	}
	if !strings.Contains(out.String(), `"backup_file":"sherwood-2026-11-22.db"`) {
		t.Errorf("no backup log line:\n%s", out.String())
	}
}

func TestNoBackupFolderWarnsAndFailsTheNightlyRun(t *testing.T) {
	sched, alerts, out, db := backupRig(t, config.Backup{Keep: 7})
	if !strings.Contains(out.String(), "no backup folder is set") {
		t.Errorf("no start-up warning:\n%s", out.String())
	}
	if err := sched.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var outcome string
	if err := db.Reader().QueryRow(`SELECT outcome FROM job_runs WHERE job_name = 'backup'`).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	sent := alerts.Sent()
	if outcome != "failed" || len(sent) != 1 || sent[0].Name() != "backup_failed" {
		t.Errorf("outcome %s, alerts %v; want failed with one backup_failed", outcome, sent)
	}
}
