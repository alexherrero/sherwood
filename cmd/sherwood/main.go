// Command sherwood is the sherwood application: it serves the phone app, runs
// the scheduled jobs and supervises the broker helper.
//
// Start-up checks everything it can before it does anything: the exchange
// calendar must cover the year, the settings and strategy files must pass
// validation, and the database must migrate cleanly. Any failure stops it
// with a non-zero exit and a message that names the problem and never holds a
// secret. Once started it serves the phone app and runs the scheduler until
// SIGTERM or SIGINT, then shuts down cleanly: the web server stops first, then
// the broker helper, then the database closes.
//
// "sherwood backtest" instead replays past bars from a folder of CSV files
// through the strategy file's rules and prints a report; it starts nothing
// else (see backtest.go).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/logging"
	"github.com/alexherrero/sherwood/internal/rules"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/store"
	"github.com/alexherrero/sherwood/internal/web"
)

// version is the build's version, set with -ldflags "-X main.version=...".
var version = "dev"

// The environment variables that can set each path, and the container
// defaults used when neither a flag nor the variable is set. The defaults sit
// in the application-data folder the Compose file mounts at /data.
const (
	envSettings     = "SHERWOOD_SETTINGS"
	envStrategy     = "SHERWOOD_STRATEGY"
	envBrokerSocket = "SHERWOOD_BROKER_SOCKET"
	envListen       = "SHERWOOD_LISTEN"

	defaultSettings = "/data/settings.yaml"
	defaultStrategy = "/data/strategy.yaml"
	// defaultListen is the web port inside the container. The Compose file
	// publishes it on the host's loopback address only.
	defaultListen = ":8080"
)

// helperCommand starts the broker helper. The image puts brokerhelper/src on
// PYTHONPATH; the supervisor appends --socket.
var helperCommand = []string{"python3", "-m", "brokerhelper"}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	err := run(ctx, env{
		args:   os.Args[1:],
		getenv: os.Getenv,
		stdout: os.Stdout,
		stderr: os.Stderr,
		clock:  clock.Real{},
	})
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sherwood:", err)
		os.Exit(1)
	}
}

// env is everything run takes from the outside world, so tests can supply
// their own.
type env struct {
	args   []string
	getenv func(string) string
	stdout io.Writer // the JSON log
	stderr io.Writer // flag usage and the broker helper's own output
	clock  clock.Clock

	// helperCommand, when set, replaces the package's helperCommand.
	helperCommand []string
	// started, when set by a test, is called once start-up has finished and
	// the scheduler is about to run, with the web server's address.
	started func(webAddr string)
}

// options are the paths start-up reads.
type options struct {
	settingsPath string
	strategyPath string
	brokerSocket string
	listen       string
}

func parseOptions(e env) (options, error) {
	fs := flag.NewFlagSet("sherwood", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	var o options
	fs.StringVar(&o.settingsPath, "settings", orDefault(e.getenv(envSettings), defaultSettings),
		"path to the app settings file (env "+envSettings+")")
	fs.StringVar(&o.strategyPath, "strategy", orDefault(e.getenv(envStrategy), defaultStrategy),
		"path to the strategy file (env "+envStrategy+")")
	fs.StringVar(&o.brokerSocket, "broker-socket",
		orDefault(e.getenv(envBrokerSocket), filepath.Join(os.TempDir(), "sherwood", "broker.sock")),
		"Unix socket for the broker helper, live mode only (env "+envBrokerSocket+")")
	fs.StringVar(&o.listen, "listen", orDefault(e.getenv(envListen), defaultListen),
		"address the web server listens on (env "+envListen+")")
	if err := fs.Parse(e.args); err != nil {
		return options{}, err
	}
	if fs.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return o, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// run starts sherwood and blocks until ctx ends or something halts it. It
// returns nil after a clean shutdown.
func run(ctx context.Context, e env) (err error) {
	if len(e.args) > 0 && e.args[0] == backtestCommand {
		return runBacktest(e, e.args[1:])
	}
	opts, err := parseOptions(e)
	if err != nil {
		return err
	}
	logger := logging.New(e.stdout, nil)

	// The holiday table must cover this year, and next year from December.
	// Refusing to start means no trading, which is safe; guessing is not.
	if err := clock.NYSE().CheckCoverage(e.clock.Now()); err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}

	settings, err := config.LoadSettings(opts.settingsPath)
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	strategy, err := config.LoadStrategy(opts.strategyPath)
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	// config cannot check rule settings (rules imports config), so the rule
	// library checks them here, before anything is opened.
	if err := rules.Validate(strategy); err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	mode := string(settings.Mode)
	logger.Info("strategy file loaded", "strategy_hash", strategy.Hash, "mode", mode)

	db, err := store.Open(ctx, settings.DatabasePath, e.clock.Now())
	if err != nil {
		return fmt.Errorf("refusing to start: opening the database: %w", err)
	}
	defer func() {
		if cerr := db.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing the database: %w", cerr)
		}
	}()
	logger.Info("database ready", "migrations", len(db.Migrated()))

	stopping := ctx // done once shutdown is requested
	ctx, cancel := context.WithCancelCause(ctx)
	var helpers sync.WaitGroup
	defer func() {
		cancel(nil)
		helpers.Wait() // the helper is stopped before the database closes
	}()
	if settings.Mode == config.ModeLive {
		sup, err := newSupervisor(e, opts, logger)
		if err != nil {
			return fmt.Errorf("refusing to start: %w", err)
		}
		helpers.Add(1)
		go func() {
			defer helpers.Done()
			_ = sup.Run(ctx) // returns ctx's error once shutdown starts
		}()
	} else {
		logger.Info("paper mode: the broker helper is not started", "mode", mode)
	}

	sched, err := scheduler.New(scheduler.Config{
		Store:    db,
		Clock:    e.clock,
		Calendar: clock.NYSE(),
		Logger:   logger,
	})
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}

	srv, err := web.New(web.Config{
		Login:           settings.TailnetLogin,
		ApprovalDevices: settings.ApprovalAddrs(),
		Clock:           e.clock,
		Logger:          logger,
		Store:           db,
		Mode:            settings.Mode,
	})
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", opts.listen)
	if err != nil {
		return fmt.Errorf("refusing to start: web server: %w", err)
	}
	webDone := make(chan error, 1)
	go func() {
		err := srv.Serve(ctx, ln)
		if err != nil {
			cancel(err) // a failed web server stops the scheduler too
		}
		webDone <- err
	}()
	logger.Info("web server listening", "listen_address", ln.Addr().String())

	logger.Info("sherwood started", "version", version, "mode", mode)
	if e.started != nil {
		e.started(ln.Addr().String())
	}
	err = sched.Run(ctx)
	cancel(nil)
	// Serve returns once requests in flight finish, before the helper stops
	// and the database closes.
	if werr := <-webDone; werr != nil {
		logger.Error("web server failed", "error", werr)
		return werr
	}
	if errors.Is(err, context.Canceled) && stopping.Err() != nil {
		logger.Info("sherwood stopped", "reason", "shutdown requested")
		return nil
	}
	logger.Error("scheduler halted", "error", err)
	return fmt.Errorf("scheduler halted: %w", err)
}

// newSupervisor builds the broker helper's supervisor. Every helper failure is
// logged; a later part also halts trading on it.
func newSupervisor(e env, opts options, logger *slog.Logger) (*broker.Supervisor, error) {
	if err := os.MkdirAll(filepath.Dir(opts.brokerSocket), 0o700); err != nil {
		return nil, fmt.Errorf("making the broker socket folder: %w", err)
	}
	cmd := helperCommand
	if e.helperCommand != nil {
		cmd = e.helperCommand
	}
	return broker.NewSupervisor(broker.SupervisorConfig{
		Command:    cmd,
		SocketPath: opts.brokerSocket,
		Clock:      e.clock,
		Stderr:     e.stderr,
		OnError: func(err error) {
			logger.Error("broker helper failed", "error", err)
		},
		OnReady: func() {
			logger.Info("broker helper ready")
		},
	})
}
