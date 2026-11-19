package broker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/alexherrero/sherwood/internal/notify/notifytest"
)

func TestLoginAlertSendsOncePerLapse(t *testing.T) {
	ctx := context.Background()
	fake := &notifytest.Fake{}
	l := &LoginAlert{Notifier: fake}
	lapsed := fmt.Errorf("holdings: %w", &HelperError{Op: "holdings", Code: CodeLoginNeeded, Message: "the broker session has expired"})
	other := &HelperError{Op: "holdings", Code: CodeInternal, Message: "x"}

	steps := []struct {
		err  error
		sent bool
	}{
		{other, false},  // not a login problem
		{lapsed, true},  // the first lapse alerts
		{lapsed, false}, // every later call stays quiet
		{lapsed, false},
		{nil, false},   // a call that works re-arms it
		{lapsed, true}, // so the next lapse alerts again
	}
	for i, s := range steps {
		sent, err := l.Seen(ctx, s.err)
		if err != nil || sent != s.sent {
			t.Fatalf("step %d: Seen = %v, %v; want %v", i, sent, err, s.sent)
		}
	}
	got := fake.Sent()
	if len(got) != 2 || got[0].Name() != "broker_login_needed" {
		t.Fatalf("alerts = %v, want two broker_login_needed", got)
	}
}

func TestLoginAlertRetriesAFailedSend(t *testing.T) {
	ctx := context.Background()
	fake := &notifytest.Fake{Err: errors.New("push service down")}
	l := &LoginAlert{Notifier: fake}
	lapsed := &HelperError{Op: "cash", Code: CodeLoginNeeded}
	if _, err := l.Seen(ctx, lapsed); err == nil {
		t.Fatal("a failed send was not reported")
	}
	fake.Err = nil
	if sent, err := l.Seen(ctx, lapsed); !sent || err != nil {
		t.Fatalf("Seen after the push service recovered = %v, %v; want it sent", sent, err)
	}
	if !LoginNeeded(lapsed) || LoginNeeded(errors.New("login_needed")) || LoginNeeded(nil) {
		t.Error("LoginNeeded reads something other than the helper's code")
	}
}
