package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
)

// RefusedKey is the field the logger adds to a line when it dropped fields
// from it. Its value lists the dropped keys, comma-separated; the dropped
// values are never written.
const RefusedKey = "refused_fields"

// Refusal describes one field the logger dropped.
type Refusal struct {
	Key    string // the field's key, with any group prefix ("req.status")
	Reason string // why it was dropped
}

// Options configures the logger. The zero value logs at Info and reports
// refusals only on the log line itself.
type Options struct {
	// Level is the minimum level written. Nil means slog.LevelInfo.
	Level slog.Leveler

	// OnRefused, when set, is called once for every field the logger drops,
	// in addition to the RefusedKey field on the line. It may be called from
	// many goroutines at once. Tests use it to fail on an unlisted field;
	// see the loggingtest package.
	OnRefused func(Refusal)
}

// Handler is a slog.Handler that writes JSON lines holding only allowlisted
// fields with plain values. A field is dropped, and its key reported, when:
//
//   - its key is not on the allowlist,
//   - it is a group, or was added under WithGroup (keys are flat),
//   - its value, after resolving any slog.LogValuer, is not a string, number,
//     bool, duration, time or error (so a header map, a request, a struct or
//     a byte slice never gets through), or
//   - it is an error that wraps a *url.Error, whose text includes the URL
//     and so may carry a token.
//
// A secret.Secret logs as its mask, under an allowlisted key, because it is a
// slog.LogValuer.
type Handler struct {
	inner      slog.Handler
	onRefused  func(Refusal)
	group      string   // dotted prefix from WithGroup, "" at the top level
	preRefused []string // keys refused by WithAttrs, reported on every line
}

// NewHandler returns a Handler writing JSON lines to w.
func NewHandler(w io.Writer, opts *Options) *Handler {
	if opts == nil {
		opts = &Options{}
	}
	return &Handler{
		inner:     slog.NewJSONHandler(w, &slog.HandlerOptions{Level: opts.Level}),
		onRefused: opts.OnRefused,
	}
}

// New returns a logger writing allowlisted JSON lines to w.
func New(w io.Writer, opts *Options) *slog.Logger {
	return slog.New(NewHandler(w, opts))
}

// Enabled reports whether the handler writes records at level.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle writes r with every refused field dropped and, if any were, a
// RefusedKey field naming them.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	refused := append([]string(nil), h.preRefused...)
	r.Attrs(func(a slog.Attr) bool {
		kept, rs := h.check(a)
		out.AddAttrs(kept...)
		for _, x := range rs {
			refused = append(refused, x.Key)
			h.report(x)
		}
		return true
	})
	if len(refused) > 0 {
		out.AddAttrs(slog.String(RefusedKey, strings.Join(refused, ",")))
	}
	return h.inner.Handle(ctx, out)
}

// WithAttrs returns a handler that adds the allowed attrs to every line and
// reports the refused ones now and on every line.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var kept []slog.Attr
	pre := append([]string(nil), h.preRefused...)
	for _, a := range attrs {
		k, rs := h.check(a)
		kept = append(kept, k...)
		for _, x := range rs {
			pre = append(pre, x.Key)
			h.report(x)
		}
	}
	return &Handler{
		inner:      h.inner.WithAttrs(kept),
		onRefused:  h.onRefused,
		group:      h.group,
		preRefused: pre,
	}
}

// WithGroup returns a handler under which every field is refused, because
// keys are flat. The empty name returns h, as slog requires.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &Handler{
		inner:      h.inner,
		onRefused:  h.onRefused,
		group:      h.group + name + ".",
		preRefused: append([]string(nil), h.preRefused...),
	}
}

func (h *Handler) report(r Refusal) {
	if h.onRefused != nil {
		h.onRefused(r)
	}
}

// check returns the attrs to keep for a, and any refusals.
func (h *Handler) check(a slog.Attr) ([]slog.Attr, []Refusal) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		if a.Key != "" {
			return nil, []Refusal{{Key: h.group + a.Key, Reason: "groups are not allowed; keys are flat"}}
		}
		// An inline group: check each of its attrs as if logged directly.
		var kept []slog.Attr
		var refused []Refusal
		for _, ga := range v.Group() {
			k, rs := h.check(ga)
			kept = append(kept, k...)
			refused = append(refused, rs...)
		}
		return kept, refused
	}
	if a.Key == "" {
		return nil, nil // slog drops these itself
	}
	key := h.group + a.Key
	if h.group != "" {
		return nil, []Refusal{{Key: key, Reason: "groups are not allowed; keys are flat"}}
	}
	if !allowed[key] {
		return nil, []Refusal{{Key: key, Reason: "key is not on the allowlist"}}
	}
	plain, reason := plainValue(v)
	if reason != "" {
		return nil, []Refusal{{Key: key, Reason: reason}}
	}
	return []slog.Attr{{Key: key, Value: plain}}, nil
}

// plainValue returns v if it is a plain value safe to write, or a reason it
// is not. Errors become their text.
func plainValue(v slog.Value) (slog.Value, string) {
	switch v.Kind() {
	case slog.KindString, slog.KindInt64, slog.KindUint64, slog.KindFloat64,
		slog.KindBool, slog.KindDuration, slog.KindTime:
		return v, ""
	case slog.KindAny:
		err, ok := v.Any().(error)
		if !ok {
			break
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			return slog.Value{}, "error wraps a URL, which may carry a token; log a reason instead"
		}
		return slog.StringValue(err.Error()), ""
	}
	return slog.Value{}, fmt.Sprintf("value of type %T is not a plain value", v.Any())
}
