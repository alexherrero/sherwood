package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
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
approval_devices: ["100.64.0.1"]
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
	if len(s.ApprovalDevices) != 1 || s.ApprovalDevices[0] != "100.64.0.1" {
		t.Errorf("approval_devices = %v", s.ApprovalDevices)
	}
	if a := s.ApprovalAddrs(); len(a) != 1 || a[0] != netip.MustParseAddr("100.64.0.1") {
		t.Errorf("ApprovalAddrs = %v", a)
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

// devices is the approval_devices line in validSettings.
const devices = `approval_devices: ["100.64.0.1"]`

func TestApprovalDevicesMustBeTailnetAddresses(t *testing.T) {
	good := []string{"100.64.0.1", "100.127.255.254", "100.100.100.100", "fd7a:115c:a1e0::1", "fd7a:115c:a1e0:ab12:4843:cd96:6258:b240"}
	for _, d := range good {
		s, err := ParseSettings([]byte(replaced(t, validSettings, devices, `approval_devices: ["`+d+`"]`)))
		if err != nil {
			t.Errorf("%s refused: %v", d, err)
			continue
		}
		if a := s.ApprovalAddrs(); len(a) != 1 || a[0] != netip.MustParseAddr(d) {
			t.Errorf("%s parsed as %v", d, a)
		}
	}
	bad := []string{
		"example-phone",          // a device name, the old form
		"100.63.255.255",         // just below the tailnet range
		"100.128.0.1",            // just above it
		"192.0.2.10",             // a documentation address
		"127.0.0.1",              // loopback
		"::ffff:100.64.0.1",      // IPv4-mapped IPv6
		"fd7a:115c:a1e1::1",      // just outside Tailscale's IPv6 prefix
		"2001:db8::1",            // a documentation IPv6 address
		"fd7a:115c:a1e0::1%eth0", // zoned
		"100.64.0.1:443",         // with a port
		"100.64.0.0/10",          // a range, not an address
		" 100.64.0.1",            // padded
	}
	for _, d := range bad {
		doc := replaced(t, validSettings, devices, fmt.Sprintf("approval_devices: [\"100.64.0.2\", %q]", d))
		refuseSettings(t, doc, "approval_devices[1] must be a device's tailnet address",
			"100.64.0.0/10", "fd7a:115c:a1e0::/48")
	}

	// A secret pasted into the list is not repeated in the refusal.
	refuseSettings(t, replaced(t, validSettings, devices, `approval_devices: ["`+planted+`"]`),
		"approval_devices[0] must be a device's tailnet address")
}

func TestBadSettingsAreRefusedByName(t *testing.T) {
	tests := []struct{ name, old, new, want string }{
		{"short feed token", planted, "short", "feed_token is too short"},
		{"empty feed token", `"` + planted + `"`, `""`, "feed_token is missing"},
		{"mode unknown", "mode: paper", "mode: practice", `mode is "practice", but it must be "paper" or "live"`},
		{"relative data dir", "data_dir: /data", "data_dir: data", `data_dir is "data", but it must be an absolute path`},
		{"relative db", "database_path: /data/sherwood.db", "database_path: sherwood.db", "database_path is \"sherwood.db\""},
		{"login with space", "tailnet_login: you@example.com", "tailnet_login: you example", "tailnet_login must not contain spaces"},
		{"empty device list", devices, "approval_devices: []", "approval_devices is missing or empty"},
		{"repeated device", devices, `approval_devices: ["100.64.0.1", "100.64.0.1"]`, `approval_devices lists "100.64.0.1" more than once`},
		{"repeated device spelled two ways", devices, `approval_devices: ["fd7a:115c:a1e0::1", "fd7a:115c:a1e0:0::1"]`, `approval_devices lists "fd7a:115c:a1e0::1" more than once`},
		{"blank device", devices, `approval_devices: [""]`, "approval_devices[0] must be a device's tailnet address"},
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
	doc := replaced(t, validSettings, devices, "approval_devices: "+planted)
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

func TestBackupSettings(t *testing.T) {
	s, err := ParseSettings([]byte(validSettings))
	if err != nil {
		t.Fatal(err)
	}
	if s.Backup.Dir != "" || s.Backup.Keep != DefaultBackupKeep {
		t.Errorf("backup with the section left out = %+v, want no folder and keep %d", s.Backup, DefaultBackupKeep)
	}

	s, err = ParseSettings([]byte(validSettings + "backup:\n  dir: /backup\n  keep: 14\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Backup.Dir != "/backup" || s.Backup.Keep != 14 {
		t.Errorf("backup = %+v, want /backup keeping 14", s.Backup)
	}

	refuseSettings(t, validSettings+"backup:\n  dir: backup\n",
		`backup.dir is "backup", but it must be an absolute path`)
	refuseSettings(t, validSettings+"backup:\n  dir: /backup\n  keep: -1\n",
		"backup.keep is -1, but it must be from 1 to 366")
	refuseSettings(t, validSettings+"backup:\n  dir: /backup\n  keep: 400\n",
		"backup.keep is 400, but it must be from 1 to 366")
	refuseSettings(t, validSettings+"backup:\n  folder: /backup\n", "folder")
}
