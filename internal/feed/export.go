package feed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/alexherrero/sherwood/internal/money"
)

// ExportVersion is the version of the research export this build serves.
const ExportVersion = 1

// MaxExportBytes is the largest export the Mac's script reads.
const MaxExportBytes = 4 << 20

// Export is the research export: what the Mac's research job may know.
//
// It carries symbols, closes and signal reasons only. It has no balances,
// share counts, cash, order details or account details, and no field that
// could hold one; task 2's allowlist test pins the JSON field names.
type Export struct {
	// Version is ExportVersion.
	Version int `json:"version"`
	// GeneratedAt is when the NAS built the export.
	GeneratedAt time.Time `json:"generated_at"`
	// SignalDate is the trading date of Signals, as 2006-01-02: the latest
	// trading date with a rules run. It is empty when there is none yet.
	SignalDate string `json:"signal_date"`
	// Symbols lists each stock held or watched, once, sorted by symbol.
	Symbols []ExportSymbol `json:"symbols"`
	// Signals lists the signals of SignalDate, in the order they were made.
	Signals []ExportSignal `json:"signals"`
}

// ExportSymbol is one stock in the export.
type ExportSymbol struct {
	Symbol string `json:"symbol"`
	// Held is whether you hold the stock. How many shares is not said.
	Held bool `json:"held"`
	// Watched is whether the stock is on the watchlist.
	Watched bool `json:"watched"`
	// Closes are the stock's recent checked closes, oldest first.
	Closes []Close `json:"closes"`
}

// Close is one checked daily close.
type Close struct {
	// Date is the trading date, as 2006-01-02.
	Date string `json:"date"`
	// Close is the closing price, as exact decimal text.
	Close money.Price `json:"close"`
}

// ExportSignal is one signal the rules made, with the rule's reason.
type ExportSignal struct {
	// Key is the signal's idempotency key (internal/signals.Key), 64
	// lowercase hex characters. A signal explanation names it.
	Key    string `json:"key"`
	Symbol string `json:"symbol"`
	// Side is buy or sell.
	Side string `json:"side"`
	// Kind is buy, add, trim, sell or stop_sell.
	Kind string `json:"kind"`
	// Reason is the rule's own reason, "<rule type>: <reason>".
	Reason string `json:"reason"`
}

// Known is what an export says about the account, as Check needs it: which
// stocks are held, and which signals exist.
type Known struct {
	// Held is the set of held symbols. A holding note must name one, and a
	// candidate must not.
	Held map[string]bool
	// Signals maps each signal's key to its symbol. A signal explanation
	// must name one of these keys, with its symbol.
	Signals map[string]string
}

// Known gives what e says about held stocks and signals, for Check. The NAS
// and the Mac both build Known this way, from the same export, so they drop
// the same items.
func (e Export) Known() Known {
	k := Known{Held: map[string]bool{}, Signals: map[string]string{}}
	for _, s := range e.Symbols {
		if s.Held {
			k.Held[s.Symbol] = true
		}
	}
	for _, s := range e.Signals {
		k.Signals[s.Key] = s.Symbol
	}
	return k
}

// DecodeExport reads an export strictly: one JSON object, no unknown
// fields, version ExportVersion, at most MaxExportBytes.
func DecodeExport(data []byte) (Export, error) {
	if len(data) > MaxExportBytes {
		return Export{}, fmt.Errorf("feed: the export is %d bytes, more than %d", len(data), MaxExportBytes)
	}
	var e Export
	if err := decodeStrict(data, &e); err != nil {
		return Export{}, fmt.Errorf("feed: the export: %w", err)
	}
	if e.Version != ExportVersion {
		return Export{}, fmt.Errorf("feed: the export has version %d, want %d", e.Version, ExportVersion)
	}
	return e, nil
}

// decodeStrict decodes exactly one JSON value into v, refusing unknown
// fields and anything after the value.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("data after the JSON value")
	}
	return nil
}
