package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alexherrero/sherwood/internal/halt"
)

// The stop switch's actions. Both are limited to the approval devices, and
// both answer with a redirect back to Today, whose ?switch= value picks one
// of switchResults to show. The value only picks fixed words; it never
// changes anything.
const (
	switchOnPath    = "/stop-switch/on"
	switchResetPath = "/stop-switch/reset"
	todayPath       = "/"

	// The reset form's fields: the newest halt the page showed, and your
	// reason.
	throughField = "through"
	reasonField  = "reason"
)

// switchResults are the words Today shows after a stop switch action.
var switchResults = map[string]notice{
	"on": {Class: "notice--warning", Title: "The stop switch is on.",
		Body: "Nothing is sent to the broker until you reset it. Rules still run and record their signals."},
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

func newSwitchView(st halt.State) switchView {
	v := switchView{On: st.On(), Newest: st.Newest()}
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
}

func backToToday(w http.ResponseWriter, r *http.Request, result string) {
	http.Redirect(w, r, todayPath+"?switch="+result, http.StatusSeeOther)
}

// switchOn throws the stop switch: you pressed Stop all trading.
func (s *Server) switchOn(w http.ResponseWriter, r *http.Request) {
	device, _ := callerAddr(r) // the device check already passed, so this is set
	evidence := "Stop all trading was pressed on the phone app, on the approval device " + device.String() + "."
	if _, _, err := s.stop.Throw(r.Context(), halt.Operator, evidence); err != nil {
		refuse(w, r, "the handler failed", http.StatusInternalServerError,
			"Sherwood could not record the stop. Try again. If it keeps failing, stop the container on the NAS.")
		return
	}
	backToToday(w, r, "on")
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
