package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/limits"
)

// Live mode needs three things before sherwood sends a real order: the
// settings file names live mode, you confirm live mode here from an approval
// device, and the paper record is at least min_paper_weeks long (see
// limits.ReadGate). Sherwood checks them when it starts, so a confirmation
// takes effect at the next restart. Settings and Today say which of the
// three is missing, and whether live trading is on now.

// confirmLivePath is the confirmation's action.
const confirmLivePath = "/settings/live"

// liveResults are the words Settings shows after a confirmation, picked by
// the ?live= value. The value only picks fixed words; it never changes
// anything.
var liveResults = map[string]pushResult{
	"confirmed": {Class: "notice--warning", Title: "Live mode is confirmed.",
		Body: "Restart sherwood on the NAS to start live trading. From then on, orders you approve are placed with the broker and use real money. A new strategy file, or a change of mode, needs a new confirmation."},
	"already": {Class: "notice--info", Title: "Nothing was changed.", Body: "Live mode is already confirmed."},
	"paper": {Class: "notice--warning", Title: "Live mode was not confirmed.",
		Body: "The settings file names paper mode. Set mode: live in the settings file on the NAS and restart first."},
	"short": {Class: "notice--warning", Title: "Live mode was not confirmed.",
		Body: "The paper record is shorter than the strategy file's minimum paper period."},
}

// liveView is the live-mode gate as Settings and Today show it.
type liveView struct {
	// Live is true when the settings file names live mode.
	Live bool
	// On is true when the live adapter was built at start-up: real orders
	// are sent.
	On bool
	// Restart is true when all three hold now but sherwood started before
	// they did, so a restart turns live trading on.
	Restart bool
	// Reasons say which of the three do not hold.
	Reasons []string
	// Confirmed is when live mode was confirmed, or "".
	Confirmed string
	// PaperWeeks and MinWeeks are the paper record's whole weeks so far and
	// the strategy file's minimum; PaperMet is true once it is long enough.
	PaperWeeks int
	MinWeeks   int
	PaperMet   bool
	// CanConfirm is true when the confirmation form is shown: live mode,
	// not yet confirmed and the paper record long enough.
	CanConfirm bool
}

// loadLive reads the live-mode gate as it stands now.
func (s *Server) loadLive(ctx context.Context, q limits.Querier) (liveView, error) {
	g, err := limits.ReadGate(ctx, q, s.strategy, s.mode, s.clock.Now())
	if err != nil {
		return liveView{}, err
	}
	v := liveView{
		Live:       s.mode == config.ModeLive,
		On:         s.liveTrading,
		Reasons:    g.Reasons(),
		PaperWeeks: g.PaperWeeks,
		MinWeeks:   g.MinWeeks,
		PaperMet:   g.PaperMet,
		CanConfirm: g.Live && !g.Confirmed && g.PaperMet,
	}
	if v.Live && !v.On {
		v.Restart = g.Open()
	}
	if g.Confirmed {
		v.Confirmed = when(g.ConfirmedAt)
	}
	return v, nil
}

// mountLive registers the confirmation. Like every other change to trading,
// it is limited to the approval devices.
func (s *Server) mountLive() {
	s.HandleChange(confirmLivePath, http.HandlerFunc(s.confirmLive), ApprovalDevicesOnly)
}

func backToLive(w http.ResponseWriter, r *http.Request, result string) {
	http.Redirect(w, r, settingsPath+"?live="+result+"#live", http.StatusSeeOther)
}

// confirmLive records your confirmation of live mode. It takes effect when
// sherwood next starts.
func (s *Server) confirmLive(w http.ResponseWriter, r *http.Request) {
	c, err := limits.ConfirmLive(r.Context(), s.store, s.strategy, s.mode, s.clock.Now())
	switch {
	case err == nil:
		s.logger.Warn("live mode confirmed", "setting", c.Setting, "strategy_hash", c.New,
			"change_source", string(limits.Phone), "reason", "live trading starts when sherwood next starts")
		backToLive(w, r, "confirmed")
	case errors.Is(err, limits.ErrConfirmed):
		backToLive(w, r, "already")
	case errors.Is(err, limits.ErrNotLive):
		backToLive(w, r, "paper")
	case errors.Is(err, limits.ErrPaperShort):
		backToLive(w, r, "short")
	default:
		refuse(w, r, "the handler failed", http.StatusInternalServerError,
			"The confirmation could not be recorded, so nothing changed. Try again.")
	}
}
