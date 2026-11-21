package main

// The setup subcommands. The setup skill (.claude/skills/strategy-setup) runs
// these on the Mac while it writes a strategy file: "rules" lists the rule
// library, "check-strategy" validates a file exactly as start-up does, and
// "fetch-bars" writes the back-tester's bars folder from the same source and
// split adjustment the after-close job uses. None of them opens a database,
// reads the settings file, starts the broker helper or reaches the NAS.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/rules"
	"github.com/alexherrero/sherwood/internal/secret"
)

const (
	rulesCommand         = "rules"
	checkStrategyCommand = "check-strategy"
	fetchBarsCommand     = "fetch-bars"

	// envTiingoToken can hold the Tiingo token for fetch-bars on the Mac. It
	// is read from the environment or a token file, never from a flag, so it
	// never sits in shell history or a process list.
	envTiingoToken = "SHERWOOD_TIINGO_TOKEN"
)

// runRules prints every rule type with its kind, what it does and each
// setting's accepted values, so the skill always works from the library the
// binary was built with.
func runRules(e env, args []string) error {
	fs := flag.NewFlagSet("sherwood rules", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("rules: unexpected argument %q", fs.Arg(0))
	}
	w := e.stdout
	for i, d := range rules.Definitions() {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s (%s rule)\n  %s\n", d.Type, d.Kind, d.Summary)
		for _, s := range d.Settings {
			fmt.Fprintf(w, "  %s (%s): %s\n", s.Name, s.Range(), s.Doc)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Every setting is required. Within one strategy: at most one sizing rule (a strategy with none never buys),")
	fmt.Fprintln(w, "size_by_risk needs a fixed_stop or trailing_stop beside it, slow_days must exceed fast_days,")
	fmt.Fprintln(w, "exit_rank must be at least top, and momentum_ranking's top must be below the number of stocks.")
	return nil
}

// runCheckStrategy loads a strategy file with the same loader and rule checks
// start-up uses, and prints a short summary. Any problem is returned, so the
// command exits non-zero and names every field at fault.
func runCheckStrategy(e env, args []string) error {
	fs := flag.NewFlagSet("sherwood check-strategy", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	strategyPath := fs.String("strategy", orDefault(e.getenv(envStrategy), defaultStrategy),
		"path to the strategy file (env "+envStrategy+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("check-strategy: unexpected argument %q", fs.Arg(0))
	}
	strategy, err := config.LoadStrategy(*strategyPath)
	if err != nil {
		return fmt.Errorf("check-strategy: %w", err)
	}
	if err := rules.Validate(strategy); err != nil {
		return fmt.Errorf("check-strategy: %w", err)
	}
	w := e.stdout
	fmt.Fprintf(w, "%s is valid. sherwood would start with it.\n", *strategyPath)
	fmt.Fprintf(w, "strategy hash: %s\n", strategy.Hash)
	for _, def := range strategy.Strategies {
		types := make([]string, len(def.Rules))
		kinds := map[rules.Kind]bool{}
		for i, r := range def.Rules {
			types[i] = r.Type
			if d, ok := rules.Lookup(r.Type); ok {
				kinds[d.Kind] = true
			}
		}
		fmt.Fprintf(w, "- %s: portfolio_share %s, stocks %s, rules %s\n",
			def.Name, def.PortfolioShare, strings.Join(def.Stocks, ", "), strings.Join(types, ", "))
		if def.IncludeWatchlist {
			fmt.Fprintf(w, "  include_watchlist: %s also runs its rules over the watchlist (%s) and any stock added to it from the app.\n",
				def.Name, strings.Join(strategy.Watchlist, ", "))
		}
		if !kinds[rules.KindSizing] {
			fmt.Fprintf(w, "  note: %s has no sizing rule, so it never buys.\n", def.Name)
		}
		if !kinds[rules.KindStop] {
			fmt.Fprintf(w, "  note: %s has no stop rule, so its holdings get no protective stop.\n", def.Name)
		}
		if !kinds[rules.KindSignal] && !kinds[rules.KindRanking] {
			fmt.Fprintf(w, "  note: %s has no signal or ranking rule, so nothing tells it when to buy.\n", def.Name)
		}
	}
	return nil
}

// runFetchBars writes one <SYMBOL>.csv per stock in the strategy file into a
// folder, in the format "sherwood backtest -bars" reads. The bars come from
// Tiingo, split-adjusted by the same code the after-close job uses (splits
// only, not dividends). Each stock costs one request; Tiingo's free plan
// allows 50 an hour.
func runFetchBars(ctx context.Context, e env, args []string) error {
	fs := flag.NewFlagSet("sherwood fetch-bars", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	strategyPath := fs.String("strategy", orDefault(e.getenv(envStrategy), defaultStrategy),
		"path to the strategy file whose stocks are fetched (env "+envStrategy+")")
	outDir := fs.String("out", "", "folder to write <SYMBOL>.csv files into; created if missing (required)")
	fromText := fs.String("from", "", "first date to fetch, YYYY-MM-DD (required)")
	toText := fs.String("to", "", "last date to fetch, YYYY-MM-DD (default: the last trading date that has closed)")
	tokenFile := fs.String("token-file", "", "file holding the Tiingo token, readable only by you (default: env "+envTiingoToken+")")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: sherwood fetch-bars -out DIR -from DATE [-to DATE] [-strategy FILE] [-token-file FILE]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("fetch-bars: unexpected argument %q", fs.Arg(0))
	}
	if *outDir == "" {
		return errors.New("fetch-bars: -out is required")
	}
	if *fromText == "" {
		return errors.New("fetch-bars: -from is required")
	}
	from, err := parseDate(fetchBarsCommand, "from", *fromText)
	if err != nil {
		return err
	}
	to, err := parseDate(fetchBarsCommand, "to", *toText)
	if err != nil {
		return err
	}
	if to.IsZero() {
		if to, err = lastClosedDate(e.clock.Now()); err != nil {
			return fmt.Errorf("fetch-bars: %w", err)
		}
	}
	if from.After(to) {
		return fmt.Errorf("fetch-bars: -from %s is after -to %s", from.Format(clock.DateLayout), to.Format(clock.DateLayout))
	}

	token, err := readTiingoToken(e, *tokenFile)
	if err != nil {
		return err
	}
	strategy, err := config.LoadStrategy(*strategyPath)
	if err != nil {
		return fmt.Errorf("fetch-bars: %w", err)
	}
	// The bars cover what a backtest runs over, the file's watchlist
	// included for a strategy that opts in to it.
	strategy = strategy.WithWatchlist(strategy.Watchlist)
	var symbols []string
	for _, def := range strategy.Strategies {
		symbols = append(symbols, def.Stocks...)
	}
	slices.Sort(symbols)
	symbols = slices.Compact(symbols)

	var source prices.Source = &prices.Tiingo{Token: token}
	if e.priceSources != nil {
		source, _ = e.priceSources(config.PriceKeys{TiingoToken: token})
	}
	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		return fmt.Errorf("fetch-bars: %w", err)
	}
	for _, sym := range symbols {
		bars, err := source.DailyBars(ctx, sym, from, to)
		if err != nil {
			return fmt.Errorf("fetch-bars: %s: %w", sym, err)
		}
		if len(bars) == 0 {
			return fmt.Errorf("fetch-bars: %s: %w between %s and %s", sym, prices.ErrNoData,
				from.Format(clock.DateLayout), to.Format(clock.DateLayout))
		}
		if err := writeBarsFile(filepath.Join(*outDir, sym+".csv"), bars); err != nil {
			return fmt.Errorf("fetch-bars: %s: %w", sym, err)
		}
		fmt.Fprintf(e.stdout, "%s: %d bars from %s to %s\n", sym, len(bars), bars[0].DateString(), bars[len(bars)-1].DateString())
	}
	fmt.Fprintf(e.stdout, "wrote %d bars files to %s (split-adjusted, not dividend-adjusted)\n", len(symbols), *outDir)
	return nil
}

// lastClosedDate is the latest trading date whose session had closed by now,
// so a fetch never asks for a bar that is still forming.
func lastClosedDate(now time.Time) (time.Time, error) {
	cal := clock.NYSE()
	if s, err := cal.Session(now); err == nil && !now.Before(s.Close) {
		return s.Date, nil
	} else if err != nil && !errors.Is(err, clock.ErrClosed) {
		return time.Time{}, err
	}
	return cal.PreviousTradingDay(now)
}

// readTiingoToken reads the token from a file the operator made, or from the
// environment. The file must be readable by its owner only.
func readTiingoToken(e env, path string) (secret.Secret, error) {
	const hint = "put your free Tiingo token in a file only you can read and pass -token-file, or set " + envTiingoToken
	if path == "" {
		v := strings.TrimSpace(e.getenv(envTiingoToken))
		if v == "" {
			return secret.Secret{}, errors.New("fetch-bars: no Tiingo token: " + hint)
		}
		return secret.New(v), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return secret.Secret{}, fmt.Errorf("fetch-bars: token file: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return secret.Secret{}, fmt.Errorf("fetch-bars: token file %s can be read by others; run chmod 600 on it", path)
	}
	b, err := os.ReadFile(path) //nolint:gosec // the operator's own token file
	if err != nil {
		return secret.Secret{}, fmt.Errorf("fetch-bars: token file: %w", err)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return secret.Secret{}, fmt.Errorf("fetch-bars: token file %s is empty: %s", path, hint)
	}
	return secret.New(v), nil
}

// writeBarsFile writes bars in the back-tester's format, through a temporary
// file so a failed run never leaves half a file behind.
func writeBarsFile(path string, bars []prices.Bar) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bars-*.csv")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := writeBarsCSV(tmp, bars); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func writeBarsCSV(w io.Writer, bars []prices.Bar) error {
	if _, err := io.WriteString(w, "Date,Open,High,Low,Close,Volume\n"); err != nil {
		return err
	}
	for _, b := range bars {
		if _, err := fmt.Fprintf(w, "%s,%s,%s,%s,%s,%d\n", b.DateString(), b.Open, b.High, b.Low, b.Close, b.Volume); err != nil {
			return err
		}
	}
	return nil
}
