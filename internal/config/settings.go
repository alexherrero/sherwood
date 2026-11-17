package config

import (
	"errors"
	"fmt"
	"net/netip"
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
	// ApprovalDevices lists the tailnet addresses of the devices that may
	// approve buys and reset the stop switch. Tailscale Serve forwards the
	// caller's tailnet address but no device name, so devices are named by
	// address. ParseSettings has checked every entry with TailnetAddr; use
	// ApprovalAddrs for the parsed form.
	ApprovalDevices []string `yaml:"approval_devices"`
	// FeedToken lets the research job read the export and upload a feed.
	// Check a presented token with FeedToken.Matches.
	FeedToken secret.Secret `yaml:"feed_token"`
	// Notifications holds the notification channels' keys.
	Notifications Notifications `yaml:"notifications"`
	// Prices holds the price sources' keys.
	Prices PriceKeys `yaml:"prices"`
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

// PriceKeys holds the keys for the two free end-of-day price sources. Both
// may be left out when the file loads, but without them every trading day's
// prices are missing and sherwood makes no signals.
type PriceKeys struct {
	// TiingoToken is the primary source's API token.
	TiingoToken secret.Secret `yaml:"tiingo_token"`
	// StooqAPIKey is the cross-check source's API key.
	StooqAPIKey secret.Secret `yaml:"stooq_api_key"`
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
	seen := make(map[netip.Addr]bool, len(s.ApprovalDevices))
	for i, d := range s.ApprovalDevices {
		a, err := TailnetAddr(d)
		switch {
		case err != nil:
			// The value is not repeated: it could be a secret pasted into
			// the wrong field.
			p.add("approval_devices[%d] must be a device's tailnet address, "+
				"an IPv4 address in %s or an IPv6 address in %s", i, tailnetIPv4, tailnetIPv6)
		case seen[a]:
			p.add("approval_devices lists %q more than once", a)
		}
		seen[a] = true
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

// The address ranges Tailscale assigns to tailnet devices: the shared CGNAT
// range for IPv4 and Tailscale's own ULA prefix for IPv6.
var (
	tailnetIPv4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetIPv6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// TailnetAddr parses s as a tailnet device address: an IPv4 address in
// 100.64.0.0/10 or an IPv6 address in fd7a:115c:a1e0::/48, written plainly
// (no zone, no port, no IPv4-mapped IPv6 form). Its errors never repeat s.
func TailnetAddr(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, errors.New("not an IP address")
	}
	// Prefix.Contains is false for a zoned or IPv4-mapped address.
	if !tailnetIPv4.Contains(a) && !tailnetIPv6.Contains(a) {
		return netip.Addr{}, fmt.Errorf("not in %s or %s", tailnetIPv4, tailnetIPv6)
	}
	return a, nil
}

// ApprovalAddrs returns ApprovalDevices parsed. Entries that are not tailnet
// addresses are left out; ParseSettings has already refused any such file.
func (s *Settings) ApprovalAddrs() []netip.Addr {
	out := make([]netip.Addr, 0, len(s.ApprovalDevices))
	for _, d := range s.ApprovalDevices {
		if a, err := TailnetAddr(d); err == nil {
			out = append(out, a)
		}
	}
	return out
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
