package notify_test

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"

	"github.com/alexherrero/sherwood/internal/notify"
)

// shortAlert is the only shape an alert's words may take: a few plain words,
// optionally starting with a count. No currency sign, no decimal point, no
// capitalised ticker, no colon introducing a reason.
var shortAlert = regexp.MustCompile(`^(?:[0-9]{1,2}\+? )?[A-Za-z][a-z]*(?: [a-z]+){0,3}$`)

func TestEveryAlertCarriesOnlyShortFixedWords(t *testing.T) {
	cases := []struct {
		alert notify.Alert
		text  string
		path  string
	}{
		{notify.Test(), "Notifications are working", "/settings"},
		{notify.TradingHalted(), "Trading halted", "/"},
		{notify.JobFailed(), "A scheduled job failed", "/"},
		{notify.JobMissed(), "Scheduled jobs ran late", "/"},
		{notify.BackupFailed(), "The nightly backup failed", "/"},
		{notify.BrokerLoginNeeded(), "Broker login needed", "/"},
		{notify.BuysWaiting(1), "1 buy waiting", "/approvals"},
		{notify.BuysWaiting(2), "2 buys waiting", "/approvals"},
		{notify.BuysWaiting(99), "99 buys waiting", "/approvals"},
		{notify.BuysWaiting(100), "99+ buys waiting", "/approvals"},
		{notify.BuysWaiting(12345), "99+ buys waiting", "/approvals"},
	}
	for _, c := range cases {
		body, err := c.alert.Payload()
		if err != nil {
			t.Fatalf("%s: %v", c.text, err)
		}
		// The payload is exactly the two fields, nothing more.
		var fields map[string]any
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 2 || fields["text"] != c.text || fields["path"] != c.path {
			t.Errorf("payload = %s, want only text %q and path %q", body, c.text, c.path)
		}
		if !shortAlert.MatchString(c.text) || len(c.text) > 32 {
			t.Errorf("alert text %q is not a short alert", c.text)
		}
	}
}

func TestAnAlertHasNoFieldForFreeText(t *testing.T) {
	// An Alert holds a kind from a fixed list and a count, and nothing else,
	// so no caller can put an amount, a stock or a reason in one.
	typ := reflect.TypeFor[notify.Alert]()
	var got []string
	for f := range typ.Fields() {
		if f.IsExported() {
			t.Errorf("Alert has an exported field %s", f.Name)
		}
		got = append(got, f.Name+" "+f.Type.String())
	}
	want := []string{"kind notify.kind", "count int"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Alert fields = %q, want %q", got, want)
	}
}

func TestTheZeroAlertIsRefused(t *testing.T) {
	var zero notify.Alert
	if zero.Valid() {
		t.Error("the zero Alert is valid")
	}
	if _, err := zero.Payload(); err == nil {
		t.Error("the zero Alert made a payload")
	}
	if notify.BuysWaiting(0).Valid() || notify.BuysWaiting(-1).Valid() {
		t.Error("BuysWaiting with no buys is valid")
	}
}
