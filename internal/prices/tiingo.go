package prices

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/secret"
)

// TiingoBaseURL is Tiingo's API address.
const TiingoBaseURL = "https://api.tiingo.com"

// Tiingo is the primary source: Tiingo's end-of-day prices API on its free
// plan, which needs a free API token. It reads the raw open, high, low, close
// and volume with each date's split factor, and adjusts for splits itself;
// Tiingo's own adjusted columns also adjust for dividends, which the rules do
// not want.
type Tiingo struct {
	// Token is the account's API token. It is sent in the Authorization
	// header, never in the URL.
	Token secret.Secret
	// BaseURL defaults to TiingoBaseURL. Tests point it at a local server.
	BaseURL string
	// Client defaults to a client with DefaultTimeout.
	Client *http.Client
}

// Name returns "tiingo".
func (*Tiingo) Name() string { return "tiingo" }

// tiingoBar is one element of Tiingo's daily prices array. Numbers are kept
// as their JSON text.
type tiingoBar struct {
	Date        string      `json:"date"`
	Open        json.Number `json:"open"`
	High        json.Number `json:"high"`
	Low         json.Number `json:"low"`
	Close       json.Number `json:"close"`
	Volume      json.Number `json:"volume"`
	SplitFactor json.Number `json:"splitFactor"`
}

// DailyBars fetches the stock's daily bars from Tiingo.
func (t *Tiingo) DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]Bar, error) {
	if err := CheckSymbol(symbol); err != nil {
		return nil, err
	}
	if t.Token.IsZero() || t.Token.Reveal() == "" {
		return nil, fmt.Errorf("tiingo: no API token configured (prices.tiingo_token)")
	}
	base := t.BaseURL
	if base == "" {
		base = TiingoBaseURL
	}
	q := url.Values{}
	q.Set("startDate", from.In(clock.Eastern).Format(clock.DateLayout))
	q.Set("endDate", to.In(clock.Eastern).Format(clock.DateLayout))
	u := strings.TrimRight(base, "/") + "/tiingo/daily/" + url.PathEscape(sourceTicker(symbol)) + "/prices?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("tiingo: building request: %s", redactedError(err))
	}
	req.Header.Set("Authorization", "Token "+t.Token.Reveal())
	req.Header.Set("Accept", "application/json")

	status, body, err := get(defaultClient(t.Client), req, "tiingo")
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusNotFound:
		return nil, fmt.Errorf("%w: tiingo does not know %s", ErrNoData, symbol)
	case status != http.StatusOK:
		return nil, fmt.Errorf("tiingo: HTTP status %d for %s", status, symbol)
	}
	return ParseTiingo(symbol, body)
}

// ParseTiingo reads a Tiingo daily prices response: a JSON array of objects
// with date, open, high, low, close, volume and splitFactor. It returns the
// bars oldest first, split-adjusted as of the last one.
func ParseTiingo(symbol string, body []byte) ([]Bar, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var rows []tiingoBar
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("%w: tiingo: not a JSON array of bars", ErrBadResponse)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: tiingo: trailing data after the bars", ErrBadResponse)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: tiingo has no bars for %s in the range", ErrNoData, symbol)
	}
	raw := make([]rawBar, len(rows))
	for i, r := range rows {
		var err error
		rb := rawBar{}
		// Tiingo writes each date as midnight UTC, such as
		// 2026-01-05T00:00:00.000Z; the calendar date is the trading date.
		if len(r.Date) < len(clock.DateLayout) || (len(r.Date) > len(clock.DateLayout) && r.Date[len(clock.DateLayout)] != 'T') {
			return nil, fmt.Errorf("%w: tiingo: bar %d has date %q", ErrBadResponse, i, r.Date)
		}
		if rb.date, err = parseDate(r.Date[:len(clock.DateLayout)]); err != nil {
			return nil, err
		}
		if rb.open, err = parseNumber("open", r.Open.String()); err != nil {
			return nil, err
		}
		if rb.high, err = parseNumber("high", r.High.String()); err != nil {
			return nil, err
		}
		if rb.low, err = parseNumber("low", r.Low.String()); err != nil {
			return nil, err
		}
		if rb.close, err = parseNumber("close", r.Close.String()); err != nil {
			return nil, err
		}
		if rb.volume, err = parseNumber("volume", r.Volume.String()); err != nil {
			return nil, err
		}
		if rb.split, err = parseNumber("splitFactor", r.SplitFactor.String()); err != nil {
			return nil, err
		}
		if !rb.split.IsPositive() {
			return nil, fmt.Errorf("%w: tiingo: bar %d has split factor 0", ErrBadResponse, i)
		}
		if i > 0 && !rb.date.After(raw[i-1].date) {
			return nil, fmt.Errorf("%w: tiingo: bars are not in date order", ErrBadResponse)
		}
		raw[i] = rb
	}
	return splitAdjust(symbol, raw)
}
