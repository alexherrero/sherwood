package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/alexherrero/sherwood/internal/execution"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/orders"
)

// The stop switch's actions. All three are limited to the approval devices,
// and all answer with a redirect back to Today, whose ?switch= value picks
// one of switchResults to show. The value only picks fixed words; it never
// changes anything.
//
// Throwing the switch halts trading, declines every buy awaiting approval,
// calls off every approved buy not yet sent and cancels every open buy at
// the broker. Resting protective stops stay. Only the second, separate
// confirmation, while the switch is on, cancels them too.
const (
	switchOnPath    = "/stop-switch/on"
	switchResetPath = "/stop-switch/reset"
	switchStopsPath = "/stop-switch/stops"
	todayPath       = "/"

	// The reset form's fields: the newest halt the page showed, and your
	// reason.
	throughField = "through"
	reasonField  = "reason"
	// confirmField must be confirmYes to cancel the protective stops.
	confirmField = "confirm"
	confirmYes   = "yes"
)

// OrderCanceller cancels open orders at the broker when the stop switch is
// thrown: the execution layer.
type OrderCanceller interface {
	CancelOpen(ctx context.Context, req execution.CancelRequest) (execution.Cancelled, error)
}

// switchResults are the words Today shows after a stop switch action.
var switchResults = map[string]notice{
	"on": {Class: "notice--warning", Title: "The stop switch is on.",
		Body: "Buys waiting for approval are declined and open buys are cancelled at the broker. Protective stops stay in place. Nothing is sent until you reset the switch; rules still run and record their signals."},
	"on_partly": {Class: "notice--warning", Title: "The stop switch is on, but not every buy could be stopped.",
		Body: "Nothing is sent until you reset the switch. Some buy could not be declined or cancelled; the halts below say which, and the Orders view shows each order's state."},
	"on_nobroker": {Class: "notice--warning", Title: "The stop switch is on.",
		Body: "Buys waiting for approval are declined. No broker is connected in this mode, so open orders could not be cancelled here; cancel them in the broker's own app."},
	"stops": {Class: "notice--warning", Title: "The protective stops are cancelled.",
		Body: "Your holdings have no stop orders now, and none is placed until you reset the switch."},
	"stops_partly": {Class: "notice--warning", Title: "Not every protective stop could be cancelled.",
		Body: "The broker did not confirm every cancellation. The halts below say which order is unknown; check it in the broker's own app."},
	"stops_confirm": {Class: "notice--warning", Title: "The protective stops were not cancelled.",
		Body: "Tick the box to confirm that your holdings will have no stop orders, then try again."},
	"stops_off": {Class: "notice--info", Title: "The protective stops were not cancelled.",
		Body: "The stop switch is off. Stop all trading first; the stops can only be cancelled while it is on."},
	"stops_nobroker": {Class: "notice--warning", Title: "The protective stops were not cancelled.",
		Body: "No broker is connected in this mode. Cancel the stops in the broker's own app."},
	"reset": {Class: "notice--info", Title: "The stop switch is reset.",
		Body: "Trading may resume. The reset is recorded with your reason and this device."},
	"reason": {Class: "notice--warning", Title: "The stop switch was not reset.",
		Body: "Write why you are resetting it, in 500 characters or fewer. The reason is kept with the reset."},
	"stale": {Class: "notice--warning", Title: "The stop switch was not reset.",
		Body: "Trading halted again after this page was loaded. Read the newest halt below, then reset again if you are sure."},
	"off": {Class: "notice--info", Title: "The stop switch was already off.", Body: "Nothing was changed."},
}

// notice is a fixed message shown at the top of a view after an action.
type notice struct {
	Class string
	Title string
	Body  string
}

// switchView is the stop switch on Today.
type switchView struct {
	On bool
	// Halts are the active halts, oldest first.
	Halts []haltRow
	// Newest is the newest active halt's id, which the reset form sends
	// back so a reset never clears a halt the page did not show.
	Newest int64
	// LastReset is the newest reset, or nil if there has been none.
	LastReset *resetRow
	// Resting counts the protective stops resting at the broker, and
	// StopsCancelled says you have cancelled them while the switch is on.
	Resting        int
	StopsCancelled bool
}

type haltRow struct {
	Cause    string
	Evidence string
	At       string
}

type resetRow struct {
	At     string
	Reason string
	Device string
}

func newSwitchView(st halt.State, resting int) switchView {
	v := switchView{On: st.On(), Newest: st.Newest(), Resting: resting, StopsCancelled: st.Has(halt.StopsCancelled)}
	for _, h := range st.Active {
		v.Halts = append(v.Halts, haltRow{Cause: h.Cause.Words(), Evidence: h.Evidence, At: when(h.At)})
	}
	if r := st.LastReset; r != nil {
		v.LastReset = &resetRow{At: when(r.At), Reason: r.Reason, Device: r.Device}
	}
	return v
}

// mountStopSwitch registers the stop switch's actions.
func (s *Server) mountStopSwitch() {
	s.HandleChange(switchOnPath, http.HandlerFunc(s.switchOn), ApprovalDevicesOnly)
	s.HandleChange(switchResetPath, http.HandlerFunc(s.switchReset), ApprovalDevicesOnly)
	s.HandleChange(switchStopsPath, http.HandlerFunc(s.switchStops), ApprovalDevicesOnly)
}

func backToToday(w http.ResponseWriter, r *http.Request, result string) {
	http.Redirect(w, r, todayPath+"?switch="+result, http.StatusSeeOther)
}

// switchOn throws the stop switch: you pressed Stop all trading. The halt is
// recorded first, so a send in progress stops before its next buy; then the
// buys waiting for approval are declined and the open buys cancelled. Each
// step writes in its own transaction, and none holds the writer while
// another throws a halt.
func (s *Server) switchOn(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithoutCancel(r.Context()) // a stop half done is still finished
	device, _ := callerAddr(r)                // the device check already passed, so this is set
	evidence := "Stop all trading was pressed on the phone app, on the approval device " + device.String() + "."
	if _, _, err := s.stop.Throw(ctx, halt.Operator, evidence); err != nil {
		refuse(w, r, "the handler failed", http.StatusInternalServerError,
			"Sherwood could not record the stop. Try again. If it keeps failing, stop the container on the NAS.")
		return
	}
	why := "You threw the stop switch on the approval device " + device.String() + "."
	result := "on"
	if _, err := execution.StopBuys(ctx, s.store, s.stop, s.clock, s.logger, why); err != nil {
		s.logger.Error("waiting buys not declined", "error", err)
		result = "on_partly"
	}
	if s.orders == nil {
		n, err := s.openOrders(ctx, orders.DayLimitBuy)
		if err != nil || n > 0 {
			result = "on_nobroker"
		}
		backToToday(w, r, result)
		return
	}
	if _, err := s.orders.CancelOpen(ctx, execution.CancelRequest{Why: why + " The broker confirmed the cancellation."}); err != nil {
		s.logger.Error("open buys not all cancelled", "error", err)
		result = "on_partly"
	}
	backToToday(w, r, result)
}

// switchStops is the second, separate confirmation: with the switch on and
// the box ticked, it cancels the resting protective stops. It first throws a
// halt of its own, so no stop is placed again until you reset the switch.
func (s *Server) switchStops(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithoutCancel(r.Context())
	device, _ := callerAddr(r)
	if r.PostForm.Get(confirmField) != confirmYes {
		backToToday(w, r, "stops_confirm")
		return
	}
	st, err := halt.Active(ctx, s.store.Reader())
	if err != nil {
		refuse(w, r, "the handler failed", http.StatusInternalServerError,
			"Sherwood could not read the stop switch, so nothing was cancelled. Try again.")
		return
	}
	if !st.On() {
		backToToday(w, r, "stops_off")
		return
	}
	if s.orders == nil {
		backToToday(w, r, "stops_nobroker")
		return
	}
	evidence := "You confirmed on the approval device " + device.String() +
		" that the protective stops are cancelled too. No stop is placed until the switch is reset."
	if _, _, err := s.stop.Throw(ctx, halt.StopsCancelled, evidence); err != nil {
		refuse(w, r, "the handler failed", http.StatusInternalServerError,
			"Sherwood could not record the confirmation, so nothing was cancelled. Try again.")
		return
	}
	why := "You cancelled the protective stops on the approval device " + device.String() +
		", after throwing the stop switch."
	if _, err := s.orders.CancelOpen(ctx, execution.CancelRequest{Stops: true, Why: why}); err != nil {
		s.logger.Error("protective stops not all cancelled", "error", err)
		backToToday(w, r, "stops_partly")
		return
	}
	backToToday(w, r, "stops")
}

// openOrders counts the orders of type t open at the broker.
func (s *Server) openOrders(ctx context.Context, t orders.Type) (int, error) {
	os, err := orders.InState(ctx, s.store.Reader(), orders.Accepted, orders.PartiallyFilled)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, o := range os {
		if o.Type == t {
			n++
		}
	}
	return n, nil
}

// switchReset resets the stop switch with your reason, if the page you sent
// it from showed the newest halt.
func (s *Server) switchReset(w http.ResponseWriter, r *http.Request) {
	device, _ := callerAddr(r)
	through, err := strconv.ParseInt(r.PostForm.Get(throughField), 10, 64)
	if err != nil {
		backToToday(w, r, "stale")
		return
	}
	_, err = s.stop.Reset(r.Context(), halt.ResetRequest{
		Through: through,
		Reason:  r.PostForm.Get(reasonField),
		Device:  device.String(),
	})
	switch {
	case err == nil:
		backToToday(w, r, "reset")
	case errors.Is(err, halt.ErrNoReason):
		backToToday(w, r, "reason")
	case errors.Is(err, halt.ErrStale):
		backToToday(w, r, "stale")
	case errors.Is(err, halt.ErrNotHalted):
		backToToday(w, r, "off")
	default:
		refuse(w, r, "the handler failed", http.StatusInternalServerError,
			"The reset could not be recorded, so the stop switch is still on. Try again.")
	}
}
