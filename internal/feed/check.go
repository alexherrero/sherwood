package feed

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Version is the version of the feed file this build reads.
const Version = 1

// Fixed caps on a feed file. A file over MaxFileBytes or MaxItems is refused;
// an item over the others is dropped.
const (
	// MaxFileBytes is the largest feed file accepted, in bytes.
	MaxFileBytes = 1 << 20
	// MaxItems is the most items one file may hold.
	MaxItems = 200
	// MaxText is the longest an item's text may be, in characters.
	MaxText = 2000
	// MaxSources is the most sources one item may cite.
	MaxSources = 10
	// MaxTitle is the longest a source's title may be, in characters.
	MaxTitle = 300
	// MaxURL is the longest a source's link may be, in bytes.
	MaxURL = 2048
)

// Kind is what an item is.
type Kind string

// The three kinds of item.
const (
	// Candidate is a case for a stock you do not hold.
	Candidate Kind = "candidate"
	// HoldingNote is what is happening to a stock you hold.
	HoldingNote Kind = "holding_note"
	// SignalExplanation is a plain-words account of why a rule made a
	// signal. It names the signal by its key.
	SignalExplanation Kind = "signal_explanation"
)

// Valid reports whether k is one of the three kinds.
func (k Kind) Valid() bool {
	return k == Candidate || k == HoldingNote || k == SignalExplanation
}

// File is a feed file, as the research job writes it.
type File struct {
	// Version is Version.
	Version int `json:"version"`
	// Items is the feed. It must be present, and may be empty.
	Items *[]Item `json:"items"`
}

// Item is one feed item.
type Item struct {
	Kind   Kind   `json:"kind"`
	Symbol string `json:"symbol"`
	// SignalKey is the explained signal's key, on a signal explanation and
	// only there.
	SignalKey string `json:"signal_key,omitempty"`
	// Text is the item's words, shown as escaped plain text.
	Text string `json:"text"`
	// Sources are the links the item rests on: at least one.
	Sources []Source `json:"sources"`
}

// Source is one link an item cites.
type Source struct {
	// URL is an http or https link.
	URL string `json:"url"`
	// Title is the page's title, or a few words saying what it is.
	Title string `json:"title"`
}

// Drop is an item Check dropped, and why.
type Drop struct {
	// Index is the item's place in the file, from 0.
	Index  int    `json:"index"`
	Kind   string `json:"kind"`
	Symbol string `json:"symbol"`
	Reason string `json:"reason"`
}

// Feed is a checked feed file: the items kept, in the file's order, and the
// items dropped.
type Feed struct {
	Items   []Item
	Dropped []Drop
}

// symbolText is the strategy file's rule for a symbol (internal/config), and
// the schema's: an uppercase letter, then letters and digits, with at most
// one '.' or '-' class suffix, at most 10 characters.
var symbolText = regexp.MustCompile(`^[A-Z][A-Z0-9]*([.-][A-Z0-9]+)?$`)

// keyText is a signal key: 64 lowercase hex characters.
var keyText = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Check reads a feed file. It refuses a file that is off the schema, with an
// error and no feed. Otherwise it keeps every item that passes the rules and
// drops the rest, each with a reason. known says which stocks are held and
// which signals exist; build it with Export.Known from the same export the
// research job was given.
func Check(data []byte, known Known) (Feed, error) {
	if len(data) > MaxFileBytes {
		return Feed{}, fmt.Errorf("feed: the file is %d bytes, more than %d", len(data), MaxFileBytes)
	}
	var f File
	if err := decodeStrict(data, &f); err != nil {
		return Feed{}, fmt.Errorf("feed: the file is not a feed: %w", err)
	}
	if f.Version != Version {
		return Feed{}, fmt.Errorf("feed: the file has version %d, want %d", f.Version, Version)
	}
	if f.Items == nil {
		return Feed{}, fmt.Errorf("feed: the file has no items list")
	}
	items := *f.Items
	if len(items) > MaxItems {
		return Feed{}, fmt.Errorf("feed: the file has %d items, more than %d", len(items), MaxItems)
	}

	out := Feed{Items: []Item{}, Dropped: []Drop{}}
	seen := map[string]bool{}
	for i, it := range items {
		reason := checkItem(it, known)
		if reason == "" {
			// One candidate and one note per stock, and one explanation per
			// signal, in each upload: the first is kept.
			id := string(it.Kind) + "\x00" + it.Symbol + "\x00" + it.SignalKey
			if seen[id] {
				reason = "an earlier item in the file says the same about this " + subject(it.Kind)
			}
			seen[id] = true
		}
		if reason != "" {
			out.Dropped = append(out.Dropped, Drop{Index: i, Kind: truncate(string(it.Kind), 40), Symbol: truncate(it.Symbol, 20), Reason: reason})
			continue
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

func subject(k Kind) string {
	if k == SignalExplanation {
		return "signal"
	}
	return "stock"
}

// checkItem gives the reason an item is dropped, or "" to keep it.
func checkItem(it Item, known Known) string {
	if !it.Kind.Valid() {
		return fmt.Sprintf("unknown kind %q", truncate(string(it.Kind), 40))
	}
	if !symbolText.MatchString(it.Symbol) || len(it.Symbol) > 10 {
		return fmt.Sprintf("symbol %q is not a stock symbol", truncate(it.Symbol, 20))
	}
	switch it.Kind {
	case Candidate:
		if it.SignalKey != "" {
			return "a candidate has no signal key"
		}
		if known.Held[it.Symbol] {
			return "a candidate must be a stock you do not hold"
		}
	case HoldingNote:
		if it.SignalKey != "" {
			return "a holding note has no signal key"
		}
		if !known.Held[it.Symbol] {
			return "a holding note must be about a stock you hold"
		}
	case SignalExplanation:
		if !keyText.MatchString(it.SignalKey) {
			return "a signal explanation needs the signal's key, 64 lowercase hex characters"
		}
		sym, ok := known.Signals[it.SignalKey]
		if !ok {
			return "no signal has this key"
		}
		if sym != it.Symbol {
			return fmt.Sprintf("the signal with this key is for %s", sym)
		}
	}
	if strings.TrimSpace(it.Text) == "" {
		return "no text"
	}
	if n := utf8.RuneCountInString(it.Text); n > MaxText {
		return fmt.Sprintf("text is %d characters, more than %d", n, MaxText)
	}
	if len(it.Sources) == 0 {
		return "no source"
	}
	if len(it.Sources) > MaxSources {
		return fmt.Sprintf("%d sources, more than %d", len(it.Sources), MaxSources)
	}
	for _, s := range it.Sources {
		if reason := checkSource(s); reason != "" {
			return reason
		}
	}
	return ""
}

// checkSource gives the reason a source makes its item dropped, or "".
func checkSource(s Source) string {
	if len(s.URL) > MaxURL {
		return fmt.Sprintf("a link is %d bytes, more than %d", len(s.URL), MaxURL)
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return "a link is not a valid URL"
	}
	if scheme := strings.ToLower(u.Scheme); scheme != "http" && scheme != "https" {
		return fmt.Sprintf("a link is not http or https: %q", truncate(u.Scheme, 20))
	}
	if u.Host == "" || u.Hostname() == "" {
		return "a link has no host"
	}
	if u.User != nil {
		return "a link carries a user name"
	}
	if strings.TrimSpace(s.Title) == "" {
		return "a source has no title"
	}
	if n := utf8.RuneCountInString(s.Title); n > MaxTitle {
		return fmt.Sprintf("a source's title is %d characters, more than %d", n, MaxTitle)
	}
	return ""
}

// truncate cuts s to at most n characters, so a drop reason quoting the
// file stays short.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
