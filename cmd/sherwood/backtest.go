package main

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/internal/backtest"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

// backtestCommand is the subcommand that runs a back-test instead of starting
// sherwood.
const backtestCommand = "backtest"

// runBacktest reads a strategy file and a folder of bars files, back-tests
// every strategy and prints the report to stdout. It opens no database, starts
// nothing and reaches no network, so it runs offline on the Mac.
func runBacktest(e env, args []string) error {
	fs := flag.NewFlagSet("sherwood backtest", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	strategyPath := fs.String("strategy", orDefault(e.getenv(envStrategy), defaultStrategy),
		"path to the strategy file (env "+envStrategy+")")
	barsDir := fs.String("bars", "", "folder of bars files, one <SYMBOL>.csv per stock with the header Date,Open,High,Low,Close (required)")
	capitalText := fs.String("capital", "", "starting capital in dollars, such as 10000.00 (required)")
	fromText := fs.String("from", "", "first trading date to replay, YYYY-MM-DD (default: the first bar)")
	toText := fs.String("to", "", "last trading date to replay, YYYY-MM-DD (default: the last bar)")
	trades := fs.Bool("trades", false, "list every filled order under each strategy")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: sherwood backtest -bars DIR -capital AMOUNT [-strategy FILE] [-from DATE] [-to DATE] [-trades]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("backtest: unexpected argument %q", fs.Arg(0))
	}
	if *barsDir == "" {
		return errors.New("backtest: -bars is required")
	}
	capital, err := money.ParseMoney(*capitalText)
	if err != nil || !capital.IsPositive() || !capital.IsCents() {
		return fmt.Errorf("backtest: -capital must be a dollar amount above zero with at most two places, such as 10000.00")
	}
	from, err := parseDate("from", *fromText)
	if err != nil {
		return err
	}
	to, err := parseDate("to", *toText)
	if err != nil {
		return err
	}

	strategy, err := config.LoadStrategy(*strategyPath)
	if err != nil {
		return fmt.Errorf("backtest: %w", err)
	}
	var symbols []string
	for _, def := range strategy.Strategies {
		symbols = append(symbols, def.Stocks...)
	}
	bars, err := backtest.LoadCSVDir(*barsDir, symbols)
	if err != nil {
		return fmt.Errorf("backtest: %w", err)
	}
	rep, err := backtest.Run(backtest.Config{Strategy: strategy, Bars: bars, Capital: capital, From: from, To: to})
	if err != nil {
		return fmt.Errorf("backtest: %w", err)
	}
	return rep.Write(e.stdout, *trades)
}

func parseDate(name, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation(clock.DateLayout, s, clock.Eastern)
	if err != nil {
		return time.Time{}, fmt.Errorf("backtest: -%s %q is not a YYYY-MM-DD date", name, s)
	}
	return t, nil
}
