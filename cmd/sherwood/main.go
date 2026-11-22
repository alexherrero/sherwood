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
// else (see backtest.go). "sherwood import-holdings" starts the broker
// helper just long enough to read the holdings and cash once, read-only, and
// records them (see importholdings.go). "sherwood rules", "sherwood
// check-strategy" and "sherwood fetch-bars" serve the setup skill on the Mac
// (see setup.go), and "sherwood research-export" and "sherwood
// research-upload" serve the nightly research job there (see research.go).
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
	"github.com/alexherrero/sherwood/internal/broker/robinhood"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/logging"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/notify/webpush"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/risk"
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
	// priceSources, when set, replaces newPriceSources, so tests never reach
	// a price source.
	priceSources func(config.PriceKeys) (primary, check prices.Source)
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
	if len(e.args) > 0 && e.args[0] == importCommand {
		return runImportHoldings(ctx, e, e.args[1:])
	}
	if len(e.args) > 0 {
		switch e.args[0] {
		case rulesCommand:
			return runRules(e, e.args[1:])
		case checkStrategyCommand:
			return runCheckStrategy(e, e.args[1:])
		case fetchBarsCommand:
			return runFetchBars(ctx, e, e.args[1:])
		case researchExportCommand:
			return runResearchExport(ctx, e, e.args[1:])
		case researchUploadCommand:
			return runResearchUpload(ctx, e, e.args[1:])
		}
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
	// A new strategy file is recorded with its limits; from then on the
	// limits tightened from the phone app under the old file no longer apply.
	if wrote, err := limits.Record(ctx, db, strategy, e.clock.Now()); err != nil {
		return fmt.Errorf("refusing to start: recording the strategy file: %w", err)
	} else if wrote {
		logger.Info("strategy file recorded", "strategy_hash", strategy.Hash, "change_source", string(limits.NAS),
			"reason", "its hash and changed limits are recorded; limits tightened from the phone app under an earlier file no longer apply")
	}
	// A new mode is recorded too; a confirmation of live mode given before
	// it, or before a new strategy file, no longer holds.
	if wrote, err := limits.RecordMode(ctx, db, settings.Mode, e.clock.Now()); err != nil {
		return fmt.Errorf("refusing to start: recording the mode: %w", err)
	} else if wrote {
		logger.Info("mode recorded", "mode", mode, "change_source", string(limits.NAS),
			"reason", "a confirmation of live mode given before this change no longer holds")
	}

	push := newPush(settings, db, e.clock, logger)
	stopSwitch, err := newStopSwitch(db, e.clock, push, logger)
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}

	stopping := ctx // done once shutdown is requested
	ctx, cancel := context.WithCancelCause(ctx)
	var helpers sync.WaitGroup
	defer func() {
		cancel(nil)
		helpers.Wait() // the helper is stopped before the database closes
	}()
	var newLive newLiveAdapter
	if settings.Mode == config.ModeLive {
		sup, err := newSupervisor(e, opts, logger, haltOnBrokerFailure(ctx, stopSwitch))
		if err != nil {
			return fmt.Errorf("refusing to start: %w", err)
		}
		newLive = robinhoodAdapter(sup, &broker.LoginAlert{Notifier: alertsOf(push)}, db, logger)
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
		Alerts:   alertsOf(push),
	})
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	if err := registerBackup(sched, settings.Backup, db, logger); err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	tr, err := registerTrading(ctx, e, sched, settings.Mode, strategy, db, stopSwitch, logger, newLive)
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	if err := registerAfterClose(e, sched, settings.Prices, strategy, db, stopSwitch, alertsOf(push), tr, logger); err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}

	var canceller web.OrderCanceller
	if tr.exec != nil {
		canceller = tr.exec
	}
	srv, err := web.New(web.Config{
		Login:           settings.TailnetLogin,
		ApprovalDevices: settings.ApprovalAddrs(),
		Clock:           e.clock,
		Logger:          logger,
		Store:           db,
		Mode:            settings.Mode,
		Push:            push,
		Switch:          stopSwitch,
		Strategy:        strategy,
		Calendar:        clock.NYSE(),
		Checker:         risk.Standard{},
		BrokerHealthy:   tr.healthy,
		FeedToken:       settings.FeedToken,
		Orders:          canceller,
		LiveTrading:     tr.live,
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

// newPush builds the web push channel from the settings file's VAPID keys.
// Keys it cannot read turn notifications off with a warning rather than stop
// sherwood: trading never depends on a notification, and Settings says
// notifications are not set up.
func newPush(settings *config.Settings, db *store.Store, c clock.Clock, logger *slog.Logger) web.PushChannel {
	wp := settings.Notifications.WebPush
	sender, err := webpush.New(webpush.Config{
		PublicKey:  wp.VAPIDPublicKey,
		PrivateKey: wp.VAPIDPrivateKey,
		Contact:    wp.Contact,
		Client:     webpush.NewHTTPClient(),
		Store:      db,
		Clock:      c,
		Logger:     logger,
	})
	if err != nil {
		logger.Warn("notifications are off", "reason", err.Error())
		return nil
	}
	return sender
}

// newStopSwitch builds the stop switch, which notifies through push, or
// nowhere when notifications are not set up.
func newStopSwitch(db *store.Store, c clock.Clock, push web.PushChannel, logger *slog.Logger) (*halt.Switch, error) {
	return halt.New(halt.Config{Store: db, Clock: c, Notifier: alertsOf(push), Logger: logger})
}

// alertsOf is push as a notifier, or nowhere when notifications are off.
func alertsOf(push web.PushChannel) notify.Notifier {
	if push == nil { // newPush returns a literal nil when notifications are off
		return notify.Nowhere
	}
	return push
}

// haltOnBrokerFailure is the supervisor's failure hook: any helper failure is
// a broker error, so trading halts. A helper that crashes and is restarted
// again and again fails each time, often in different words (another exit
// status, a start that timed out), so the halt is thrown with ThrowOnce: one
// halt and one push while a broker_error halt is active, however many
// restarts fail. Every failure is still logged by the supervisor's hook. A
// failure to record the halt is logged by the switch.
func haltOnBrokerFailure(ctx context.Context, sw *halt.Switch) func(error) {
	return func(err error) {
		_, _, _ = sw.ThrowOnce(ctx, halt.BrokerError, "The broker helper failed: "+err.Error())
	}
}

// robinhoodAdapter returns the builder of the live broker adapter over the
// supervisor's helper. Every broker call's outcome goes through the one login
// alert, so an expired session sends one alert however many calls fail.
func robinhoodAdapter(sup *broker.Supervisor, login *broker.LoginAlert, db *store.Store, logger *slog.Logger) newLiveAdapter {
	return func() (liveAdapter, error) {
		return robinhood.New(robinhood.Config{Client: sup.Client(), Helper: sup, Login: login,
			Keys: robinhood.OrderKeys(db.Reader()), Logger: logger})
	}
}

// newSupervisor builds the broker helper's supervisor. Every helper failure is
// logged and passed to onFailure, which halts trading.
func newSupervisor(e env, opts options, logger *slog.Logger, onFailure func(error)) (*broker.Supervisor, error) {
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
			onFailure(err)
		},
		OnReady: func() {
			logger.Info("broker helper ready")
		},
	})
}
