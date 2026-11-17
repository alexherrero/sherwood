package prices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/secret"
)

// madeUpKey is an obviously fake key for the test servers.
const madeUpKey = "example-key-not-real"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // a fixture name from this file
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(clock.DateLayout, s, clock.Eastern)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// barText renders bars compactly for comparison.
func barText(bars []Bar) string {
	var sb strings.Builder
	for _, b := range bars {
		fmt.Fprintf(&sb, "%s %s %s %s %s %s %d\n", b.Symbol, b.DateString(), b.Open, b.High, b.Low, b.Close, b.Volume)
	}
	return sb.String()
}

func TestParseTiingoAdjustsForSplits(t *testing.T) {
	bars, err := ParseTiingo("AAPL", fixture(t, "tiingo_daily.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The two-for-one split on 7 January halves the earlier prices and
	// doubles their volumes. 103.125 rounds half away from zero; the raw
	// 102.554 rounds to the cent.
	want := "" +
		"AAPL 2026-01-05 100.00 105.00 99.00 102.00 2000000\n" +
		"AAPL 2026-01-06 102.25 103.13 100.55 102.50 2400002\n" +
		"AAPL 2026-01-07 102.10 103.00 101.00 102.55 2500000\n"
	if got := barText(bars); got != want {
		t.Errorf("bars:\n%s\nwant:\n%s", got, want)
	}
	for _, b := range bars {
		if !b.Date.Equal(clock.DateOf(b.Date)) {
			t.Errorf("%s is not midnight Eastern", b.Date)
		}
	}
}

func TestParseTiingoReverseSplitVolumeRoundsDown(t *testing.T) {
	body := `[
	  {"date":"2026-01-05T00:00:00.000Z","open":1.5,"high":1.6,"low":1.4,"close":1.55,"volume":1005,"splitFactor":1.0},
	  {"date":"2026-01-06T00:00:00.000Z","open":15.5,"high":16,"low":15,"close":15.75,"volume":100,"splitFactor":0.1}
	]`
	bars, err := ParseTiingo("MSFT", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := "" +
		"MSFT 2026-01-05 15.00 16.00 14.00 15.50 100\n" +
		"MSFT 2026-01-06 15.50 16.00 15.00 15.75 100\n"
	if got := barText(bars); got != want {
		t.Errorf("bars:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseTiingoRefusesBadShapes(t *testing.T) {
	good := `{"date":"2026-01-05T00:00:00.000Z","open":1,"high":2,"low":1,"close":1.5,"volume":10,"splitFactor":1.0}`
	tests := []struct {
		name, body string
		want       error
	}{
		{"empty array", `[]`, ErrNoData},
		{"not json", `<html></html>`, ErrBadResponse},
		{"an object", `{"detail":"Error: made up"}`, ErrBadResponse},
		{"exponent", strings.Replace(`[`+good+`]`, `"close":1.5`, `"close":1.5e2`, 1), ErrBadResponse},
		{"negative", strings.Replace(`[`+good+`]`, `"open":1`, `"open":-1`, 1), ErrBadResponse},
		{"null price", strings.Replace(`[`+good+`]`, `"close":1.5`, `"close":null`, 1), ErrBadResponse},
		{"missing split factor", strings.Replace(`[`+good+`]`, `,"splitFactor":1.0`, ``, 1), ErrBadResponse},
		{"zero split factor", strings.Replace(`[`+good+`]`, `"splitFactor":1.0`, `"splitFactor":0`, 1), ErrBadResponse},
		{"bad date", strings.Replace(`[`+good+`]`, `2026-01-05T`, `2026-13-05T`, 1), ErrBadResponse},
		{"date with junk", strings.Replace(`[`+good+`]`, `2026-01-05T00:00:00.000Z`, `2026-01-05X`, 1), ErrBadResponse},
		{"out of order", `[` + good + `,` + good + `]`, ErrBadResponse},
		{"trailing data", `[` + good + `] []`, ErrBadResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseTiingo("AAPL", []byte(tt.body))
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseStooq(t *testing.T) {
	bars, err := ParseStooq("AAPL", fixture(t, "stooq_daily.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "" +
		"AAPL 2026-01-05 100.10 105.20 99.05 102.30 2000100\n" +
		"AAPL 2026-01-06 102.25 103.10 100.50 102.45 2400000\n" +
		"AAPL 2026-01-07 102.20 103.05 100.95 102.60 2510000\n"
	if got := barText(bars); got != want {
		t.Errorf("bars:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseStooqRefusesOtherAnswers(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want error
	}{
		{"no data", fixture(t, "stooq_no_data.txt"), ErrNoData},
		{"empty", nil, ErrNoData},
		{"header only", []byte("Date,Open,High,Low,Close,Volume\n"), ErrNoData},
		{"daily limit", fixture(t, "stooq_limit.txt"), ErrBadResponse},
		{"web page", fixture(t, "stooq_no_key.html"), ErrBadResponse},
		{"wrong header", []byte("Date,Open,High,Low,Close\n2026-01-05,1,2,1,1.5\n"), ErrBadResponse},
		{"short row", []byte("Date,Open,High,Low,Close,Volume\n2026-01-05,1,2,1,1.5\n"), ErrBadResponse},
		{"exponent", []byte("Date,Open,High,Low,Close,Volume\n2026-01-05,1,2,1,1.5e1,10\n"), ErrBadResponse},
		{"bad date", []byte("Date,Open,High,Low,Close,Volume\n05/01/2026,1,2,1,1.5,10\n"), ErrBadResponse},
		{"out of order", []byte("Date,Open,High,Low,Close,Volume\n2026-01-06,1,2,1,1.5,10\n2026-01-05,1,2,1,1.5,10\n"), ErrBadResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseStooq("AAPL", tt.body)
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseStooqAcceptsByteOrderMark(t *testing.T) {
	body := append([]byte("\ufeff"), fixture(t, "stooq_daily.csv")...)
	if _, err := ParseStooq("AAPL", body); err != nil {
		t.Fatal(err)
	}
}

func TestTiingoRequest(t *testing.T) {
	var gotPath, gotAuth string
	var gotQuery map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotQuery = r.URL.Path, r.Header.Get("Authorization"), r.URL.Query()
		_, _ = w.Write(fixture(t, "tiingo_daily.json"))
	}))
	defer srv.Close()
	src := &Tiingo{Token: secret.New(madeUpKey), BaseURL: srv.URL, Client: srv.Client()}
	bars, err := src.DailyBars(context.Background(), "BRK.B", day(t, "2026-01-05"), day(t, "2026-01-07"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 3 || bars[0].Symbol != "BRK.B" {
		t.Errorf("bars = %v", barText(bars))
	}
	if gotPath != "/tiingo/daily/brk-b/prices" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Token "+madeUpKey {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if fmt.Sprint(gotQuery) != "map[endDate:[2026-01-07] startDate:[2026-01-05]]" {
		t.Errorf("query = %v (the token must not be in it)", gotQuery)
	}
}

func TestTiingoStatuses(t *testing.T) {
	status := http.StatusNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(fixture(t, "tiingo_not_found.json"))
	}))
	defer srv.Close()
	src := &Tiingo{Token: secret.New(madeUpKey), BaseURL: srv.URL, Client: srv.Client()}
	d := day(t, "2026-01-05")
	if _, err := src.DailyBars(context.Background(), "AAPL", d, d); !errors.Is(err, ErrNoData) {
		t.Errorf("404: err = %v, want ErrNoData", err)
	}
	status = http.StatusTooManyRequests
	_, err := src.DailyBars(context.Background(), "AAPL", d, d)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("429: err = %v", err)
	}
}

func TestStooqRequest(t *testing.T) {
	var gotPath string
	var gotQuery map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		_, _ = w.Write(fixture(t, "stooq_daily.csv"))
	}))
	defer srv.Close()
	src := &Stooq{Key: secret.New(madeUpKey), BaseURL: srv.URL, Client: srv.Client()}
	bars, err := src.DailyBars(context.Background(), "BRK.B", day(t, "2026-01-05"), day(t, "2026-01-07"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 3 || bars[2].Symbol != "BRK.B" {
		t.Errorf("bars = %v", barText(bars))
	}
	if gotPath != "/q/d/l/" {
		t.Errorf("path = %q", gotPath)
	}
	want := "map[apikey:[" + madeUpKey + "] d1:[20260105] d2:[20260107] i:[d] s:[brk-b.us]]"
	if fmt.Sprint(gotQuery) != want {
		t.Errorf("query = %v, want %v", gotQuery, want)
	}
}

func TestStooqErrorsNeverCarryTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	src := &Stooq{Key: secret.New(madeUpKey), BaseURL: srv.URL, Client: srv.Client()}
	d := day(t, "2026-01-05")
	_, err := src.DailyBars(context.Background(), "AAPL", d, d)
	if err == nil || strings.Contains(err.Error(), madeUpKey) {
		t.Errorf("HTTP 500: err = %v", err)
	}
	// A closed server gives a transport error, whose URL would carry the key.
	srv.Close()
	_, err = src.DailyBars(context.Background(), "AAPL", d, d)
	if err == nil || strings.Contains(err.Error(), madeUpKey) || strings.Contains(err.Error(), "apikey") {
		t.Errorf("transport error: err = %v", err)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		t.Errorf("the error still wraps the URL error: %v", err)
	}
}

func TestSourcesRefuseWithoutKeyOrGoodSymbol(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("no request should be sent")
	}))
	defer srv.Close()
	d := day(t, "2026-01-05")
	for _, src := range []Source{&Tiingo{BaseURL: srv.URL}, &Stooq{BaseURL: srv.URL}} {
		if _, err := src.DailyBars(context.Background(), "AAPL", d, d); err == nil {
			t.Errorf("%s: no key should be refused", src.Name())
		}
	}
	for _, src := range []Source{&Tiingo{Token: secret.New(madeUpKey), BaseURL: srv.URL}, &Stooq{Key: secret.New(madeUpKey), BaseURL: srv.URL}} {
		if _, err := src.DailyBars(context.Background(), "aapl/../x", d, d); err == nil {
			t.Errorf("%s: a bad symbol should be refused", src.Name())
		}
	}
}

func TestOversizedResponseRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", maxBody+10)))
	}))
	defer srv.Close()
	src := &Tiingo{Token: secret.New(madeUpKey), BaseURL: srv.URL, Client: srv.Client()}
	d := day(t, "2026-01-05")
	if _, err := src.DailyBars(context.Background(), "AAPL", d, d); !errors.Is(err, ErrBadResponse) {
		t.Errorf("err = %v, want ErrBadResponse", err)
	}
}
