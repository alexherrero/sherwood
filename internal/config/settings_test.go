package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/secret"
)

// Fake values only. planted is a stand-in secret that tests require never to
// appear in an error or in printed output.
const (
	planted     = "planted-value-0123456789abcdefghijklmnop"
	plantedVAPI = "planted-vapid-private-value-zzzz"
)

var validSettings = `tailnet_login: you@example.com
approval_devices: [example-phone]
feed_token: "` + planted + `"
notifications:
  web_push:
    vapid_public_key: "example-vapid-public-key"
    vapid_private_key: "` + plantedVAPI + `"
    contact: "mailto:you@example.com"
mode: paper
data_dir: /data
database_path: /data/sherwood.db
`

func refuseSettings(t *testing.T, doc string, want ...string) {
	t.Helper()
	s, err := ParseSettings([]byte(doc))
	if err == nil {
		t.Fatalf("ParseSettings accepted the file: %+v", s)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error does not wrap ErrInvalid: %v", err)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error does not mention %q:\n%v", w, err)
		}
	}
	assertNoSecret(t, err.Error())
}

func assertNoSecret(t *testing.T, text string) {
	t.Helper()
	for _, s := range []string{planted, plantedVAPI, "planted"} {
		if strings.Contains(text, s) {
			t.Errorf("output reveals a secret value:\n%s", text)
		}
	}
}

func TestValidSettingsParse(t *testing.T) {
	s, err := ParseSettings([]byte(validSettings))
	if err != nil {
		t.Fatal(err)
	}
	if s.TailnetLogin != "you@example.com" || s.Mode != ModePaper ||
		s.DataDir != "/data" || s.DatabasePath != "/data/sherwood.db" {
		t.Errorf("settings = %+v", s)
	}
	if len(s.ApprovalDevices) != 1 || s.ApprovalDevices[0] != "example-phone" {
		t.Errorf("approval_devices = %v", s.ApprovalDevices)
	}
	if !s.FeedToken.Matches(planted) || s.FeedToken.Matches("other") {
		t.Error("feed token not read as a secret")
	}
	wp := s.Notifications.WebPush
	if wp.VAPIDPublicKey != "example-vapid-public-key" || wp.Contact != "mailto:you@example.com" ||
		wp.VAPIDPrivateKey.Reveal() != plantedVAPI {
		t.Errorf("web push = %+v", wp)
	}

	// Printing or encoding the settings shows only masks.
	js, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{fmt.Sprintf("%v", s), fmt.Sprintf("%+v", *s), fmt.Sprintf("%#v", *s), string(js)} {
		assertNoSecret(t, out)
		if !strings.Contains(out, secret.Mask) {
			t.Errorf("expected the mask in %s", out)
		}
	}

	live, err := ParseSettings([]byte(replaced(t, validSettings, "mode: paper", "mode: live")))
	if err != nil || live.Mode != ModeLive {
		t.Errorf("live mode: %v, %v", live, err)
	}
}

func TestEachMissingSettingIsRefusedByName(t *testing.T) {
	tests := []struct{ line, want string }{
		{"tailnet_login:", "tailnet_login is missing"},
		{"approval_devices:", "approval_devices is missing or empty"},
		{"feed_token:", "feed_token is missing"},
		{"    vapid_public_key:", "notifications.web_push.vapid_public_key is missing"},
		{"    vapid_private_key:", "notifications.web_push.vapid_private_key is missing"},
		{"    contact:", "notifications.web_push.contact is missing"},
		{"mode:", "mode is missing"},
		{"data_dir:", "data_dir is missing"},
		{"database_path:", "database_path is missing"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			refuseSettings(t, withoutLine(t, validSettings, tc.line), tc.want)
		})
	}
}

func TestBadSettingsAreRefusedByName(t *testing.T) {
	tests := []struct{ name, old, new, want string }{
		{"short feed token", planted, "short", "feed_token is too short"},
		{"empty feed token", `"` + planted + `"`, `""`, "feed_token is missing"},
		{"mode unknown", "mode: paper", "mode: practice", `mode is "practice", but it must be "paper" or "live"`},
		{"relative data dir", "data_dir: /data", "data_dir: data", `data_dir is "data", but it must be an absolute path`},
		{"relative db", "database_path: /data/sherwood.db", "database_path: sherwood.db", "database_path is \"sherwood.db\""},
		{"login with space", "tailnet_login: you@example.com", "tailnet_login: you example", "tailnet_login must not contain spaces"},
		{"empty device list", "approval_devices: [example-phone]", "approval_devices: []", "approval_devices is missing or empty"},
		{"repeated device", "approval_devices: [example-phone]", "approval_devices: [example-phone, example-phone]", `approval_devices lists "example-phone" more than once`},
		{"blank device", "approval_devices: [example-phone]", `approval_devices: [""]`, "approval_devices[0] must be a device name"},
		{"contact scheme", `contact: "mailto:you@example.com"`, `contact: "you@example.com"`, "contact must start with mailto: or https://"},
		{"unknown field", "mode: paper", "mode: paper\nbroker_password: x", "field broker_password not found"},
		{"secret as list", `feed_token: "` + planted + `"`, `feed_token: ["` + planted + `"]`, "secret: value must be a string"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			refuseSettings(t, replaced(t, validSettings, tc.old, tc.new), tc.want)
		})
	}
}

func TestSettingsErrorsNeverRepeatAValue(t *testing.T) {
	// A secret pasted into the wrong field makes the YAML library build a
	// type error that quotes the value; the error must not carry it.
	doc := replaced(t, validSettings, "approval_devices: [example-phone]", "approval_devices: "+planted)
	refuseSettings(t, doc, "line 2", "(value hidden)")

	doc = replaced(t, validSettings, "    contact: \"mailto:you@example.com\"", "    contact:\n      - "+planted)
	refuseSettings(t, doc)
}

func TestSettingsStructuralProblems(t *testing.T) {
	refuseSettings(t, "", "the file is empty")
	refuseSettings(t, validSettings+"---\nmode: live\n", "more than one YAML document")
	refuseSettings(t, replaced(t, validSettings, "mode: paper", "mode: paper\nmode: live"), "already defined")
}

func TestLoadSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := os.WriteFile(path, []byte(validSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSettings(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSettings(filepath.Join(t.TempDir(), "missing.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: err = %v, want one wrapping os.ErrNotExist", err)
	}
}

func TestSettingsExampleValidates(t *testing.T) {
	s, err := LoadSettings(filepath.Join("..", "..", "settings.example.yaml"))
	if err != nil {
		t.Fatalf("settings.example.yaml does not validate: %v", err)
	}
	if s.Mode != ModePaper {
		t.Errorf("the example should start in paper mode, got %q", s.Mode)
	}
}
