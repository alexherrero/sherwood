package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
)

// writeBars writes made-up bars for each symbol into dir: a slow climb with
// a dip, enough for the example strategy's longest rule.
func writeBars(t *testing.T, dir string, symbols ...string) {
	t.Helper()
	day := time.Date(2024, time.January, 2, 0, 0, 0, 0, clock.Eastern)
	for n, sym := range symbols {
		var b strings.Builder
		b.WriteString("Date,Open,High,Low,Close,Volume\n")
		c := int64(4000 + n*1500)
		for i := range 220 {
			step := int64((i*37+n*11)%23) - 10
			if i%60 > 50 {
				step -= 25
			}
			open := c
			c += step
			hi, lo := max(open, c)+12, min(open, c)-12
			fmt.Fprintf(&b, "%s,%d.%02d,%d.%02d,%d.%02d,%d.%02d,1000\n", day.AddDate(0, 0, i).Format(clock.DateLayout),
				open/100, open%100, hi/100, hi%100, lo/100, lo%100, c/100, c%100)
		}
		if err := os.WriteFile(filepath.Join(dir, sym+".csv"), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func backtestRun(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(context.Background(), env{
		args:   append([]string{"backtest"}, args...),
		getenv: func(string) string { return "" },
		stdout: &out,
		stderr: &errOut,
		clock:  clock.NewFake(covered),
	})
	return out.String(), errOut.String(), err
}

func TestBacktestSubcommand(t *testing.T) {
	f := writeExamples(t, nil, nil)
	bars := filepath.Join(f.dir, "bars")
	if err := os.Mkdir(bars, 0o700); err != nil {
		t.Fatal(err)
	}
	writeBars(t, bars, "AAPL", "MSFT", "KO")
	args := []string{"-strategy", f.strategy, "-bars", bars, "-capital", "10000.00", "-trades"}
	first, _, err := backtestRun(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Sherwood back-test", "not financial advice", "\nexample-trend\n", "\nexample-pullback\n",
		"Return:", "Max drawdown:", "Win rate:", "Turnover:"} {
		if !strings.Contains(first, want) {
			t.Errorf("the report has no %q:\n%s", want, first)
		}
	}
	again, _, err := backtestRun(t, args...)
	if err != nil || again != first {
		t.Errorf("a second run differs (err %v):\n%s", err, again)
	}
	if _, err := os.Stat(f.database); !os.IsNotExist(err) {
		t.Errorf("the back-test touched the database: %v", err)
	}
	if strings.Contains(first, "sherwood started") {
		t.Error("the back-test started sherwood")
	}
}

func TestBacktestRefusesBadArguments(t *testing.T) {
	f := writeExamples(t, nil, nil)
	for name, args := range map[string][]string{
		"no bars":      {"-strategy", f.strategy, "-capital", "100.00"},
		"no capital":   {"-strategy", f.strategy, "-bars", f.dir},
		"bad capital":  {"-strategy", f.strategy, "-bars", f.dir, "-capital", "1e4"},
		"sub-cent":     {"-strategy", f.strategy, "-bars", f.dir, "-capital", "100.001"},
		"bad date":     {"-strategy", f.strategy, "-bars", f.dir, "-capital", "100.00", "-from", "yesterday"},
		"missing bars": {"-strategy", f.strategy, "-bars", f.dir, "-capital", "100.00"},
		"extra arg":    {"-strategy", f.strategy, "-bars", f.dir, "-capital", "100.00", "now"},
	} {
		if out, _, err := backtestRun(t, args...); err == nil {
			t.Errorf("%s: no error; printed:\n%s", name, out)
		}
	}
}
