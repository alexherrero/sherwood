package prices

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/secret"
)

// StooqBaseURL is Stooq's address.
const StooqBaseURL = "https://stooq.com"

// Stooq is the cross-check source: Stooq's daily CSV download, which needs a
// free API key. Stooq adjusts its history itself, so only the newest bar of a
// range is certain to be on the same basis as another source's; the Fetcher
// compares only the trading date's own bar.
type Stooq struct {
	// Key is the API key. Stooq takes it in the query string, so errors from
	// this source never include the URL.
	Key secret.Secret
	// BaseURL defaults to StooqBaseURL. Tests point it at a local server.
	BaseURL string
	// Client defaults to a client with DefaultTimeout.
	Client *http.Client
}

// Name returns "stooq".
func (*Stooq) Name() string { return "stooq" }

// stooqDateLayout is the date form Stooq's query takes.
const stooqDateLayout = "20060102"

// DailyBars fetches the stock's daily bars from Stooq.
func (s *Stooq) DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]Bar, error) {
	if err := CheckSymbol(symbol); err != nil {
		return nil, err
	}
	if s.Key.IsZero() || s.Key.Reveal() == "" {
		return nil, fmt.Errorf("stooq: no API key configured (prices.stooq_api_key)")
	}
	base := s.BaseURL
	if base == "" {
		base = StooqBaseURL
	}
	q := url.Values{}
	q.Set("s", sourceTicker(symbol)+".us")
	q.Set("d1", from.In(clock.Eastern).Format(stooqDateLayout))
	q.Set("d2", to.In(clock.Eastern).Format(stooqDateLayout))
	q.Set("i", "d")
	q.Set("apikey", s.Key.Reveal())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/q/d/l/?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("stooq: building request failed")
	}
	req.Header.Set("Accept", "text/csv")

	status, body, err := get(defaultClient(s.Client), req, "stooq")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("stooq: HTTP status %d for %s", status, symbol)
	}
	return ParseStooq(symbol, body)
}

// stooqHeader is the header row of Stooq's daily CSV.
var stooqHeader = []string{"Date", "Open", "High", "Low", "Close", "Volume"}

// ParseStooq reads a Stooq daily CSV: a Date,Open,High,Low,Close,Volume header
// and one row per trading date, oldest first. Stooq answers "No data" for an
// unknown symbol or an empty range, and plain text or a web page when the key
// is missing or the daily limit is reached; none of those is CSV.
func ParseStooq(symbol string, body []byte) ([]Bar, error) {
	text := strings.TrimSpace(string(body))
	switch {
	case text == "" || strings.EqualFold(text, "No data"):
		return nil, fmt.Errorf("%w: stooq has no bars for %s in the range", ErrNoData, symbol)
	case strings.Contains(text, "Exceeded the daily hits limit"):
		return nil, fmt.Errorf("%w: stooq: the daily request limit is reached", ErrBadResponse)
	}
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = len(stooqHeader)
	r.ReuseRecord = false
	header, err := r.Read()
	if err != nil || !equalHeader(header) {
		return nil, fmt.Errorf("%w: stooq: the response is not the daily CSV (check the API key)", ErrBadResponse)
	}
	one := decimal.NewFromInt(1)
	var raw []rawBar
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: stooq: row %d is not CSV", ErrBadResponse, len(raw)+1)
		}
		rb := rawBar{split: one}
		if rb.date, err = parseDate(rec[0]); err != nil {
			return nil, err
		}
		if rb.open, err = parseNumber("open", rec[1]); err != nil {
			return nil, err
		}
		if rb.high, err = parseNumber("high", rec[2]); err != nil {
			return nil, err
		}
		if rb.low, err = parseNumber("low", rec[3]); err != nil {
			return nil, err
		}
		if rb.close, err = parseNumber("close", rec[4]); err != nil {
			return nil, err
		}
		if rb.volume, err = parseNumber("volume", rec[5]); err != nil {
			return nil, err
		}
		if n := len(raw); n > 0 && !rb.date.After(raw[n-1].date) {
			return nil, fmt.Errorf("%w: stooq: rows are not in date order", ErrBadResponse)
		}
		raw = append(raw, rb)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: stooq has no bars for %s in the range", ErrNoData, symbol)
	}
	// Stooq's history is already adjusted, so every split factor is 1.
	return splitAdjust(symbol, raw)
}

func equalHeader(h []string) bool {
	if len(h) != len(stooqHeader) {
		return false
	}
	for i := range h {
		// The first field may carry a byte-order mark.
		if strings.TrimPrefix(strings.TrimSpace(h[i]), "\ufeff") != stooqHeader[i] {
			return false
		}
	}
	return true
}
