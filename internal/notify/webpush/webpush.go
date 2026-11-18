// Package webpush sends alerts as web push messages to the installed phone
// app (RFC 8030), encrypted for each subscription (RFC 8291) and signed with
// the VAPID key pair from the settings file (RFC 8292).
//
// The encryption and signing come from github.com/ergochat/webpush-go/v2.
// This package adds what sherwood needs around it: subscriptions kept in the
// database, endpoints limited to the known push services, a removed
// subscription dropped when its push service says it is gone, and errors and
// log lines that never hold an endpoint or a key.
package webpush

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	wp "github.com/ergochat/webpush-go/v2"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/secret"
	"github.com/alexherrero/sherwood/internal/store"
)

const (
	// MaxSubscriptions caps how many devices can turn notifications on, so
	// one alert never fans out to an unbounded list.
	MaxSubscriptions = 10
	// MaxEndpointLen is the longest endpoint accepted.
	MaxEndpointLen = 2048
	// TTL is how long a push service keeps an alert for a phone that is off.
	TTL = 12 * time.Hour
	// SendTimeout bounds each send to a push service.
	SendTimeout = 15 * time.Second
	// vapidLifetime is how long each signed VAPID token is valid. RFC 8292
	// allows at most 24 hours.
	vapidLifetime = 12 * time.Hour
)

// pushServices are the push services a subscription's endpoint may point at:
// Apple's (iPhone), Google's (Android Chrome), Mozilla's (Firefox) and
// Microsoft's (Edge). Limiting endpoints to them means sherwood never sends a
// request anywhere else, such as to the NAS's own network. A leading dot
// matches any name under that domain.
var pushServices = []string{
	".push.apple.com",
	"fcm.googleapis.com",
	".push.services.mozilla.com",
	".notify.windows.com",
}

// ErrBadSubscription means the browser's subscription is not one sherwood
// can send to: the endpoint is not https at a known push service, or a key
// is not the right shape.
var ErrBadSubscription = errors.New("webpush: the subscription is not one sherwood can send to")

// ErrTooManySubscriptions means MaxSubscriptions devices already have
// notifications on.
var ErrTooManySubscriptions = fmt.Errorf("webpush: at most %d devices can turn notifications on", MaxSubscriptions)

// HTTPClient sends a request to a push service. NewHTTPClient is the real
// one; tests pass a fake, so no test reaches a push service.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// NewHTTPClient returns the client the running app sends pushes with: a
// timeout per request, no redirects followed and no proxy.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: SendTimeout,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: SendTimeout,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          4,
			IdleConnTimeout:       90 * time.Second,
		},
		// A push service answers directly; a redirect could point anywhere.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Config is what a Sender needs.
type Config struct {
	// PublicKey and PrivateKey are the VAPID key pair from the settings file,
	// each base64url: the public key is the 65-byte uncompressed P-256 point
	// and the private key the 32-byte scalar.
	PublicKey  string
	PrivateKey secret.Secret
	// Contact is the mailto: or https: address push services may use to
	// reach the sender.
	Contact string
	// Client sends the requests. It is required, so a test must choose a
	// fake; the running app passes NewHTTPClient().
	Client HTTPClient
	Store  *store.Store
	Clock  clock.Clock
	Logger *slog.Logger
}

// Sender is the web push channel. It implements notify.Notifier.
type Sender struct {
	keys    *wp.VAPIDKeys
	contact string
	client  HTTPClient
	store   *store.Store
	clock   clock.Clock
	logger  *slog.Logger
}

var _ notify.Notifier = (*Sender)(nil)

// New checks the key pair and builds a Sender. Its errors name the settings
// field and never hold a key.
func New(cfg Config) (*Sender, error) {
	if cfg.Client == nil || cfg.Store == nil || cfg.Clock == nil || cfg.Logger == nil {
		return nil, errors.New("webpush: a client, the database, a clock and a logger are required")
	}
	if !strings.HasPrefix(cfg.Contact, "mailto:") && !strings.HasPrefix(cfg.Contact, "https://") {
		return nil, errors.New("webpush: notifications.web_push.contact must start with mailto: or https://")
	}
	keys, err := wp.DecodeLegacyVAPIDPrivateKey(cfg.PrivateKey.Reveal())
	if err != nil {
		return nil, errors.New("webpush: notifications.web_push.vapid_private_key is not a VAPID private key " +
			"(32 bytes, base64url)")
	}
	pub, err := decodeKey(cfg.PublicKey)
	if err != nil || base64.RawURLEncoding.EncodeToString(pub) != keys.PublicKeyString() {
		return nil, errors.New("webpush: notifications.web_push.vapid_public_key is not the public half of " +
			"vapid_private_key")
	}
	return &Sender{
		keys:    keys,
		contact: cfg.Contact,
		client:  cfg.Client,
		store:   cfg.Store,
		clock:   cfg.Clock,
		logger:  cfg.Logger,
	}, nil
}

// PublicKey is the VAPID public key, base64url without padding, which the
// app passes to the browser as applicationServerKey when it subscribes.
func (s *Sender) PublicKey() string { return s.keys.PublicKeyString() }

// Subscription is what the browser's PushSubscription gives: the endpoint
// and the two keys, base64url as getKey returns them.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// Subscribe records sub, replacing the keys of an existing subscription with
// the same endpoint. It returns ErrBadSubscription or
// ErrTooManySubscriptions without writing anything.
func (s *Sender) Subscribe(ctx context.Context, sub Subscription) error {
	if !allowedEndpoint(sub.Endpoint) {
		return ErrBadSubscription
	}
	keys, err := wp.DecodeSubscriptionKeys(sub.Auth, sub.P256dh)
	if err != nil {
		return ErrBadSubscription
	}
	p256dh := base64.RawURLEncoding.EncodeToString(keys.P256dh.Bytes())
	auth := base64.RawURLEncoding.EncodeToString(keys.Auth[:])
	now := store.FormatTime(s.clock.Now())

	tx, err := s.store.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("webpush: saving the subscription: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var others int
	err = tx.QueryRowContext(ctx,
		`SELECT count(*) FROM push_subscriptions WHERE endpoint <> ?`, sub.Endpoint).Scan(&others)
	if err != nil {
		return fmt.Errorf("webpush: saving the subscription: %w", err)
	}
	if others >= MaxSubscriptions {
		return ErrTooManySubscriptions
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO push_subscriptions (endpoint, p256dh, auth, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (endpoint) DO UPDATE SET
			p256dh = excluded.p256dh, auth = excluded.auth, updated_at = excluded.updated_at`,
		sub.Endpoint, p256dh, auth, now, now)
	if err != nil {
		return fmt.Errorf("webpush: saving the subscription: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("webpush: saving the subscription: %w", err)
	}
	s.logger.Info("push subscription saved", "push_subscriptions", others+1)
	return nil
}

// Count reports how many devices have notifications on.
func (s *Sender) Count(ctx context.Context) (int, error) {
	var n int
	err := s.store.Reader().QueryRowContext(ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("webpush: counting subscriptions: %w", err)
	}
	return n, nil
}

type subscription struct {
	id       int64
	endpoint string
	keys     wp.Keys
}

// Notify sends a to every subscription. A subscription its push service
// says is gone (404 or 410) is deleted. Sends are not retried: an alert that
// failed shows in the log, and the next alert tries again.
func (s *Sender) Notify(ctx context.Context, a notify.Alert) error {
	payload, err := a.Payload()
	if err != nil {
		return err
	}
	subs, err := s.subscriptions(ctx)
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return notify.ErrNoRecipients
	}
	var delivered, failed, removed int
	for _, sub := range subs {
		switch s.send(ctx, payload, a, sub) {
		case sent:
			delivered++
		case gone:
			if err := s.remove(ctx, sub.id); err != nil {
				failed++
				continue
			}
			removed++
		default:
			failed++
		}
	}
	args := []any{"push_alert", a.Name(), "push_delivered", delivered, "push_failed", failed, "push_removed", removed}
	switch {
	case failed > 0:
		s.logger.Warn("push not delivered everywhere", args...)
		return fmt.Errorf("webpush: %d of %d sends failed", failed, len(subs))
	case delivered == 0:
		s.logger.Warn("push not delivered", args...)
		return fmt.Errorf("webpush: every subscription had ended and was removed: %w", notify.ErrNoRecipients)
	}
	s.logger.Info("push sent", args...)
	return nil
}

func (s *Sender) subscriptions(ctx context.Context) ([]subscription, error) {
	rows, err := s.store.Reader().QueryContext(ctx,
		`SELECT id, endpoint, p256dh, auth FROM push_subscriptions ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("webpush: reading subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []subscription
	for rows.Next() {
		var sub subscription
		var p256dh, auth string
		if err := rows.Scan(&sub.id, &sub.endpoint, &p256dh, &auth); err != nil {
			return nil, fmt.Errorf("webpush: reading subscriptions: %w", err)
		}
		if sub.keys, err = wp.DecodeSubscriptionKeys(auth, p256dh); err != nil {
			return nil, errors.New("webpush: a stored subscription's keys are unreadable")
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("webpush: reading subscriptions: %w", err)
	}
	return out, nil
}

func (s *Sender) remove(ctx context.Context, id int64) error {
	_, err := s.store.Writer().ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("webpush: removing an ended subscription: %w", err)
	}
	return nil
}

type outcome int

const (
	failedSend outcome = iota
	sent
	gone
)

// send makes one push request. Its result is an outcome, not an error,
// because the library's and the client's errors can carry the endpoint.
func (s *Sender) send(ctx context.Context, payload []byte, a notify.Alert, sub subscription) outcome {
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	resp, err := wp.SendNotification(ctx, payload, &wp.Subscription{Endpoint: sub.endpoint, Keys: sub.keys}, &wp.Options{
		HTTPClient: s.client,
		Subscriber: s.contact,
		// Topic lets the push service replace an undelivered alert of the
		// same kind with the newer one.
		Topic:           a.Name(),
		TTL:             int(TTL / time.Second),
		Urgency:         wp.UrgencyHigh,
		VAPIDKeys:       s.keys,
		VapidExpiration: s.clock.Now().Add(vapidLifetime),
	})
	if err != nil {
		return failedSend
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return sent
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return gone
	}
	s.logger.Warn("push service refused an alert", "push_alert", a.Name(), "status", resp.StatusCode)
	return failedSend
}

// allowedEndpoint reports whether endpoint is an https URL at one of the
// known push services, on the default port, with no user part.
func allowedEndpoint(endpoint string) bool {
	if len(endpoint) > MaxEndpointLen || !strings.HasPrefix(endpoint, "https://") {
		return false
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	host := u.Hostname()
	if host != strings.ToLower(host) {
		return false
	}
	for _, svc := range pushServices {
		if strings.HasPrefix(svc, ".") {
			if strings.HasSuffix(host, svc) && len(host) > len(svc) {
				return true
			}
		} else if host == svc {
			return true
		}
	}
	return false
}

// decodeKey reads a base64url key, with or without padding.
func decodeKey(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}
