package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexherrero/sherwood/internal/secret"
)

// Mode is the trading mode.
type Mode string

// The two trading modes.
const (
	ModePaper Mode = "paper"
	ModeLive  Mode = "live"
)

// MinFeedTokenLen is the shortest feed token accepted.
const MinFeedTokenLen = 32

// Settings is a validated app settings file. It holds no broker secrets; those
// are the broker helper's alone.
type Settings struct {
	// TailnetLogin is the one tailnet login sherwood accepts.
	TailnetLogin string `yaml:"tailnet_login"`
	// ApprovalDevices names the tailnet devices that may approve buys and
	// reset the stop switch.
	ApprovalDevices []string `yaml:"approval_devices"`
	// FeedToken lets the research job read the export and upload a feed.
	// Check a presented token with FeedToken.Matches.
	FeedToken secret.Secret `yaml:"feed_token"`
	// Notifications holds the notification channels' keys.
	Notifications Notifications `yaml:"notifications"`
	// Mode is paper or live.
	Mode Mode `yaml:"mode"`
	// DataDir is the application-data folder.
	DataDir string `yaml:"data_dir"`
	// DatabasePath is the SQLite database file.
	DatabasePath string `yaml:"database_path"`
}

// Notifications holds the keys for each notification channel.
type Notifications struct {
	WebPush WebPush `yaml:"web_push"`
}

// WebPush holds the VAPID key pair and contact address for web push.
type WebPush struct {
	VAPIDPublicKey  string        `yaml:"vapid_public_key"`
	VAPIDPrivateKey secret.Secret `yaml:"vapid_private_key"`
	// Contact is the mailto: or https: address push services may use to
	// reach the sender.
	Contact string `yaml:"contact"`
}

// LoadSettings reads and validates the app settings file at path.
func LoadSettings(path string) (*Settings, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own configured file
	if err != nil {
		return nil, fmt.Errorf("reading settings file: %w", err)
	}
	return ParseSettings(data)
}

// ParseSettings validates settings file bytes. Errors name the field and never
// repeat a secret's value.
func ParseSettings(data []byte) (*Settings, error) {
	var s Settings
	if err := decodeStrict(data, &s); err != nil {
		return nil, &ValidationError{File: "settings file", Problems: []string{err.Error()}}
	}
	p := &problems{file: "settings file"}

	if s.TailnetLogin == "" {
		p.add("tailnet_login is missing")
	} else if strings.ContainsFunc(s.TailnetLogin, isSpace) {
		p.add("tailnet_login must not contain spaces")
	}

	if len(s.ApprovalDevices) == 0 {
		p.add("approval_devices is missing or empty; name at least one device, such as your phone")
	}
	seen := make(map[string]bool, len(s.ApprovalDevices))
	for i, d := range s.ApprovalDevices {
		switch {
		case d == "" || strings.ContainsFunc(d, isSpace):
			p.add("approval_devices[%d] must be a device name with no spaces", i)
		case seen[d]:
			p.add("approval_devices lists %q more than once", d)
		}
		seen[d] = true
	}

	if s.FeedToken.IsZero() || s.FeedToken.Reveal() == "" {
		p.add("feed_token is missing")
	} else if len(s.FeedToken.Reveal()) < MinFeedTokenLen {
		p.add("feed_token is too short; use at least %d random characters", MinFeedTokenLen)
	}

	wp := s.Notifications.WebPush
	if wp.VAPIDPublicKey == "" {
		p.add("notifications.web_push.vapid_public_key is missing")
	}
	if wp.VAPIDPrivateKey.IsZero() || wp.VAPIDPrivateKey.Reveal() == "" {
		p.add("notifications.web_push.vapid_private_key is missing")
	}
	switch {
	case wp.Contact == "":
		p.add("notifications.web_push.contact is missing")
	case !strings.HasPrefix(wp.Contact, "mailto:") && !strings.HasPrefix(wp.Contact, "https://"):
		p.add("notifications.web_push.contact must start with mailto: or https://")
	}

	switch s.Mode {
	case "":
		p.add("mode is missing; set it to %q or %q", ModePaper, ModeLive)
	case ModePaper, ModeLive:
	default:
		p.add("mode is %q, but it must be %q or %q", s.Mode, ModePaper, ModeLive)
	}

	checkAbsPath(p, "data_dir", s.DataDir)
	checkAbsPath(p, "database_path", s.DatabasePath)

	if err := p.err(); err != nil {
		return nil, err
	}
	return &s, nil
}

func checkAbsPath(p *problems, field, path string) {
	switch {
	case path == "":
		p.add("%s is missing", field)
	case !filepath.IsAbs(path):
		p.add("%s is %q, but it must be an absolute path", field, path)
	}
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}
