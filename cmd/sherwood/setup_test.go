package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/backtest"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/prices/pricestest"
	"github.com/alexherrero/sherwood/internal/rules"
)

// skillDir is the setup skill's folder, relative to this package.
var skillDir = filepath.Join("..", "..", ".claude", "skills", "strategy-setup")

// fakeToken stands in for a Tiingo token. It must never reach the output.
const fakeToken = "fake-tiingo-token-for-tests-only" //nolint:gosec // a made-up value, not a credential

func runCmd(t *testing.T, e env, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	e.args = args
	if e.getenv == nil {
		e.getenv = func(string) string { return "" }
	}
	e.stdout, e.stderr = &out, &errOut
	if e.clock == nil {
		e.clock = clock.NewFake(covered)
	}
	err := run(context.Background(), e)
	return out.String(), errOut.String(), err
}

func TestRulesListsEveryRuleType(t *testing.T) {
	out, _, err := runCmd(t, env{}, "rules")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range rules.Definitions() {
		if !strings.Contains(out, d.Type+" ("+string(d.Kind)+" rule)") {
			t.Errorf("rules output lacks %s", d.Type)
		}
		for _, s := range d.Settings {
			if !strings.Contains(out, "  "+s.Name+" ("+s.Range()+"): ") {
				t.Errorf("rules output lacks %s's setting %s", d.Type, s.Name)
			}
		}
	}
}

func TestCheckStrategyAcceptsTheExample(t *testing.T) {
	f := writeExamples(t, nil, nil)
	out, _, err := runCmd(t, env{}, "check-strategy", "-strategy", f.strategy)
	if err != nil {
		t.Fatal(err)
	}
	s, err := config.LoadStrategy(f.strategy)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"is valid", "strategy hash: " + s.Hash, "- example-trend: portfolio_share 0.40, stocks AAPL, MSFT"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "note:") {
		t.Errorf("the example should give no notes:\n%s", out)
	}
}

func TestCheckStrategyNamesBadRuleSettings(t *testing.T) {
	f := writeExamples(t, nil, func(s string) string {
		return replaceOnce(t, s, "          high_days: 20\n", "")
	})
	out, _, err := runCmd(t, env{}, "check-strategy", "-strategy", f.strategy)
	if !errors.Is(err, config.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), `strategy "example-pullback": rules[0] (pullback): high_days is missing`) {
		t.Errorf("error does not name the setting: %v", err)
	}
	if out != "" {
		t.Errorf("a failed check printed %q", out)
	}
}

func TestCheckStrategyNotesAStrategyThatCannotBuyOrStop(t *testing.T) {
	f := writeExamples(t, nil, func(s string) string {
		s = replaceOnce(t, s, "      - type: trailing_stop\n        settings:\n          distance: 0.10\n", "")
		return replaceOnce(t, s, "      - type: size_fixed_amount\n        settings:\n          amount: \"100.00\"\n", "")
	})
	out, _, err := runCmd(t, env{}, "check-strategy", "-strategy", f.strategy)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"note: example-trend has no sizing rule, so it never buys.", "note: example-trend has no stop rule"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// The skill's example output must pass the same checks start-up runs.
func TestSkillExampleStrategyValidates(t *testing.T) {
	path := filepath.Join(skillDir, "example-strategy.yaml")
	s, err := config.LoadStrategy(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.Validate(s); err != nil {
		t.Fatal(err)
	}
	out, _, err := runCmd(t, env{}, "check-strategy", "-strategy", path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "note:") {
		t.Errorf("the skill's example should give no notes:\n%s", out)
	}
	// Its stocks are made-up placeholders such as TECH1, not real symbols.
	placeholder := regexp.MustCompile(`^[A-Z]{4}[0-9]$`)
	for _, def := range s.Strategies {
		for _, sym := range def.Stocks {
			if !placeholder.MatchString(sym) {
				t.Errorf("strategy %s: %q is not a placeholder symbol", def.Name, sym)
			}
		}
	}
	if len(s.Watchlist) != 0 {
		t.Errorf("watchlist = %v, want empty", s.Watchlist)
	}
}

// The skill must stay in step with the rule library and the commands it runs.
func TestSkillNamesEveryRuleTypeAndCommand(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md")) //nolint:gosec // a fixed file in this repo
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, name := range rules.Types() {
		if !strings.Contains(text, "`"+name+"`") {
			t.Errorf("SKILL.md does not mention rule type %s", name)
		}
	}
	for _, cmd := range []string{rulesCommand, checkStrategyCommand, fetchBarsCommand, backtestCommand} {
		if !strings.Contains(text, `"$WORK/sherwood" `+cmd) {
			t.Errorf("SKILL.md does not run sherwood %s", cmd)
		}
	}
	if !strings.Contains(text, "not financial advice") {
		t.Error("SKILL.md does not say its output is not financial advice")
	}
}

// A privacy scan of the skill: no email address, home folder, key-like
// string or broker host in anything it ships.
func TestSkillHasNothingPersonal(t *testing.T) {
	bad := map[string]*regexp.Regexp{
		"email address": regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),
		"home folder":   regexp.MustCompile(`/Users/[A-Za-z]|/home/[A-Za-z]`),
		"key-like text": regexp.MustCompile(`[A-Za-z0-9_\-]{32,}`),
		"broker host":   regexp.MustCompile(`(?i)robinhood\.com`),
	}
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the skill folder is empty")
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(skillDir, e.Name())) //nolint:gosec // a fixed file in this repo
		if err != nil {
			t.Fatal(err)
		}
		for what, re := range bad {
			if m := re.Find(b); m != nil {
				t.Errorf("%s holds %s: %q", e.Name(), what, m)
			}
		}
	}
}

// fakeTiingo returns an env whose fetch-bars source is src, and records the
// token it was given.
func fakeTiingo(src prices.Source, gotToken *string) env {
	return env{priceSources: func(k config.PriceKeys) (prices.Source, prices.Source) {
		*gotToken = k.TiingoToken.Reveal()
		return src, nil
	}}
}

func TestFetchBarsRoundTripsThroughTheBacktester(t *testing.T) {
	f := writeExamples(t, nil, nil)
	symbols := []string{"AAPL", "KO", "MSFT"}
	orig := filepath.Join(f.dir, "orig")
	if err := os.Mkdir(orig, 0o700); err != nil {
		t.Fatal(err)
	}
	writeBars(t, orig, symbols...)
	loaded, err := backtest.LoadCSVDir(orig, symbols)
	if err != nil {
		t.Fatal(err)
	}
	src := pricestest.New("tiingo")
	for _, sym := range symbols {
		for _, b := range loaded[sym] {
			b.Symbol = sym
			src.Add(b)
		}
	}
	var token string
	e := fakeTiingo(src, &token)
	e.getenv = func(k string) string {
		if k == envTiingoToken {
			return fakeToken
		}
		return ""
	}
	out := filepath.Join(f.dir, "bars")
	stdout, stderr, err := runCmd(t, e, "fetch-bars", "-strategy", f.strategy, "-out", out, "-from", "2024-01-01", "-to", "2024-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if token != fakeToken {
		t.Errorf("source got token %q, want the environment's", token)
	}
	if strings.Contains(stdout+stderr, fakeToken) {
		t.Error("the token reached the output")
	}
	for _, sym := range symbols {
		want, _ := os.ReadFile(filepath.Join(orig, sym+".csv")) //nolint:gosec // a test temp file
		got, err := os.ReadFile(filepath.Join(out, sym+".csv")) //nolint:gosec // a test temp file
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s.csv differs from the bars the source gave", sym)
		}
	}
	if !strings.Contains(stdout, "wrote 3 bars files") || !strings.Contains(stdout, "AAPL: 220 bars from 2024-01-02") {
		t.Errorf("unexpected output:\n%s", stdout)
	}
	report, _, err := backtestRun(t, "-strategy", f.strategy, "-bars", out, "-capital", "10000.00")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "example-trend") {
		t.Errorf("back-test report lacks the strategy:\n%s", report)
	}
}

func TestFetchBarsDefaultsToTheLastClosedDate(t *testing.T) {
	f := writeExamples(t, nil, nil)
	src := pricestest.New("tiingo")
	for _, sym := range []string{"AAPL", "KO", "MSFT"} {
		src.Add(pricestest.Bar(sym, "2026-11-13", "10.00", "10.50", "9.50", "10.25", 100))
	}
	var token string
	e := fakeTiingo(src, &token)
	e.getenv = func(k string) string {
		if k == envTiingoToken {
			return fakeToken
		}
		return ""
	}
	// covered is 10:00 Eastern on Monday 16 November 2026, before the close,
	// so the last closed date is Friday the 13th.
	if _, _, err := runCmd(t, e, "fetch-bars", "-strategy", f.strategy, "-out", filepath.Join(f.dir, "bars"), "-from", "2026-11-01"); err != nil {
		t.Fatal(err)
	}
	calls := src.Calls()
	if len(calls) != 3 {
		t.Fatalf("calls = %v, want one per stock", calls)
	}
	for _, c := range calls {
		if c.From != "2026-11-01" || c.To != "2026-11-13" {
			t.Errorf("call %+v, want 2026-11-01 to 2026-11-13", c)
		}
	}

	if got, err := lastClosedDate(covered.Add(7 * time.Hour)); err != nil || got.Format(clock.DateLayout) != "2026-11-16" {
		t.Errorf("after the close: %v, %v; want 2026-11-16", got, err)
	}
}

func TestFetchBarsTokenFile(t *testing.T) {
	f := writeExamples(t, nil, nil)
	src := pricestest.New("tiingo")
	for _, sym := range []string{"AAPL", "KO", "MSFT"} {
		src.Add(pricestest.Bar(sym, "2026-11-13", "10.00", "10.50", "9.50", "10.25", 100))
	}
	tokenFile := filepath.Join(f.dir, "tiingo-token")
	if err := os.WriteFile(tokenFile, []byte(fakeToken+"\n"), 0o644); err != nil { //nolint:gosec // testing the refusal
		t.Fatal(err)
	}
	var token string
	args := []string{"fetch-bars", "-strategy", f.strategy, "-out", filepath.Join(f.dir, "bars"), "-from", "2026-11-01", "-token-file", tokenFile}
	_, _, err := runCmd(t, fakeTiingo(src, &token), args...)
	if err == nil || !strings.Contains(err.Error(), "can be read by others") {
		t.Fatalf("err = %v, want a refusal of a readable token file", err)
	}
	if len(src.Calls()) != 0 {
		t.Error("the source was called with a refused token file")
	}
	if err := os.Chmod(tokenFile, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCmd(t, fakeTiingo(src, &token), args...)
	if err != nil {
		t.Fatal(err)
	}
	if token != fakeToken {
		t.Errorf("source got token %q, want the file's, trimmed", token)
	}
	if strings.Contains(stdout+stderr, fakeToken) {
		t.Error("the token reached the output")
	}
}

func TestFetchBarsRefusals(t *testing.T) {
	f := writeExamples(t, nil, nil)
	bars := filepath.Join(f.dir, "bars")
	withToken := func(k string) string {
		if k == envTiingoToken {
			return fakeToken
		}
		return ""
	}
	cases := []struct {
		name   string
		getenv func(string) string
		args   []string
		want   string
	}{
		{"no token", nil, []string{"-out", bars, "-from", "2026-11-01"}, "no Tiingo token"},
		{"no out", withToken, []string{"-from", "2026-11-01"}, "-out is required"},
		{"no from", withToken, []string{"-out", bars}, "-from is required"},
		{"bad date", withToken, []string{"-out", bars, "-from", "1/11/2026"}, `fetch-bars: -from "1/11/2026" is not a YYYY-MM-DD date`},
		{"backwards", withToken, []string{"-out", bars, "-from", "2026-11-10", "-to", "2026-11-01"}, "is after -to"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := pricestest.New("tiingo")
			var token string
			e := fakeTiingo(src, &token)
			e.getenv = c.getenv
			_, _, err := runCmd(t, e, append([]string{"fetch-bars", "-strategy", f.strategy}, c.args...)...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if len(src.Calls()) != 0 {
				t.Error("the source was called")
			}
		})
	}
}

func TestFetchBarsStopsOnASourceError(t *testing.T) {
	f := writeExamples(t, nil, nil)
	src := pricestest.New("tiingo")
	src.Add(pricestest.Bar("AAPL", "2026-11-13", "10.00", "10.50", "9.50", "10.25", 100))
	src.Fail("KO", prices.ErrBadResponse)
	var token string
	e := fakeTiingo(src, &token)
	e.getenv = func(k string) string {
		if k == envTiingoToken {
			return fakeToken
		}
		return ""
	}
	bars := filepath.Join(f.dir, "bars")
	_, _, err := runCmd(t, e, "fetch-bars", "-strategy", f.strategy, "-out", bars, "-from", "2026-11-01")
	if !errors.Is(err, prices.ErrBadResponse) || !strings.Contains(err.Error(), "KO") {
		t.Fatalf("err = %v, want KO's source error", err)
	}
	if _, err := os.Stat(filepath.Join(bars, "KO.csv")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("KO.csv was written after its source failed: %v", err)
	}
	entries, _ := os.ReadDir(bars)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".bars-") {
			t.Errorf("left a temporary file %s", e.Name())
		}
	}
}
