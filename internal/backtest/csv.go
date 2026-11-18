package backtest

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/prices"
)

// ErrCSV is wrapped by the error a bars file gives when it cannot be read.
var ErrCSV = errors.New("backtest: bad bars file")

// csvHeader is the header a bars file must start with. A Volume column after
// Close is allowed and read.
var csvHeader = []string{"Date", "Open", "High", "Low", "Close"}

// plainNumber is the only number text accepted in a bars file.
var plainNumber = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// maxBarsFile bounds a bars file's size.
const maxBarsFile = 16 << 20

// LoadCSVDir reads each symbol's bars from dir/<SYMBOL>.csv. A file has the
// header Date,Open,High,Low,Close (optionally ,Volume), one row per trading
// date, oldest first, with dates as YYYY-MM-DD and prices as plain decimals.
// Prices with more than two places are rounded to the cent, halves away from
// zero, as the written rounding policy says. The bars must already be
// split-adjusted. Each row is checked: the high is the highest price and the
// low the lowest.
func LoadCSVDir(dir string, symbols []string) (map[string][]prices.Bar, error) {
	out := map[string][]prices.Bar{}
	for _, sym := range slices.Sorted(slices.Values(symbols)) {
		if _, done := out[sym]; done {
			continue
		}
		if err := prices.CheckSymbol(sym); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, sym+".csv")
		f, err := os.Open(path) //nolint:gosec // the operator's own bars folder and a checked symbol
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrCSV, sym, err)
		}
		bars, err := ReadCSV(sym, io.LimitReader(f, maxBarsFile))
		cerr := f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if cerr != nil {
			return nil, cerr
		}
		out[sym] = bars
	}
	return out, nil
}

// ReadCSV reads one symbol's bars in the bars file format.
func ReadCSV(symbol string, r io.Reader) ([]prices.Bar, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = false
	head, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: reading the header: %w", ErrCSV, err)
	}
	volume := len(head) == len(csvHeader)+1 && strings.TrimSpace(head[len(csvHeader)]) == "Volume"
	if (len(head) != len(csvHeader) && !volume) || !slices.Equal(trimAll(head[:min(len(head), len(csvHeader))]), csvHeader) {
		return nil, fmt.Errorf("%w: the header must be %s (optionally ,Volume), not %s",
			ErrCSV, strings.Join(csvHeader, ","), strings.Join(head, ","))
	}
	var bars []prices.Bar
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCSV, err)
		}
		if len(rec) != len(head) {
			return nil, fmt.Errorf("%w: line %d has %d fields, want %d", ErrCSV, line, len(rec), len(head))
		}
		b, err := readRow(symbol, trimAll(rec), volume)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %w", ErrCSV, line, err)
		}
		if n := len(bars); n > 0 && !b.Date.After(bars[n-1].Date) {
			return nil, fmt.Errorf("%w: line %d: %s is not after %s", ErrCSV, line, b.DateString(), bars[n-1].DateString())
		}
		bars = append(bars, b)
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("%w: no bars", ErrCSV)
	}
	return bars, nil
}

func trimAll(rec []string) []string {
	out := make([]string, len(rec))
	for i, s := range rec {
		out[i] = strings.TrimSpace(s)
	}
	return out
}

func readRow(symbol string, rec []string, volume bool) (prices.Bar, error) {
	date, err := time.ParseInLocation(clock.DateLayout, rec[0], clock.Eastern)
	if err != nil {
		return prices.Bar{}, fmt.Errorf("date %q is not YYYY-MM-DD", rec[0])
	}
	b := prices.Bar{Symbol: symbol, Date: date}
	for i, dst := range []*money.Price{&b.Open, &b.High, &b.Low, &b.Close} {
		if *dst, err = readPrice(csvHeader[i+1], rec[i+1]); err != nil {
			return prices.Bar{}, err
		}
	}
	if volume && rec[5] != "" {
		v, err := strconv.ParseInt(rec[5], 10, 64)
		if err != nil || v < 0 {
			return prices.Bar{}, fmt.Errorf("volume %q is not a whole number", rec[5])
		}
		b.Volume = v
	}
	if err := b.Check(); err != nil {
		return prices.Bar{}, err
	}
	return b, nil
}

func readPrice(field, s string) (money.Price, error) {
	if len(s) > 32 || !plainNumber.MatchString(s) {
		return money.Price{}, fmt.Errorf("%s %q is not a plain decimal", field, s)
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return money.Price{}, fmt.Errorf("%s %q is not a plain decimal", field, s)
	}
	p, err := money.RoundPrice(d)
	if err != nil {
		return money.Price{}, fmt.Errorf("%s: %w", field, err)
	}
	return p, nil
}
