package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TokenField is the hidden form field that carries the anti-forgery token.
const TokenField = "csrf_token"

// TokenLifetime is how long an anti-forgery token stays valid. A page left
// open longer must be reloaded before its forms work again.
const TokenLifetime = 12 * time.Hour

// tokenSkew is how far in the future a token's issue time may be, to allow
// for the clock stepping back a little.
const tokenSkew = time.Minute

// sessionCookieName uses the __Host- prefix, so the browser keeps the cookie
// only for this exact host, over HTTPS, for every path.
const sessionCookieName = "__Host-sherwood_session"

// sessionMaxAge is how long the browser keeps the session cookie.
const sessionMaxAge = 7 * 24 * time.Hour

// sessionBytes is the session identifier's length before encoding.
const sessionBytes = 32

// tokener issues and checks anti-forgery tokens. A token is the time it was
// issued and an HMAC over that time, the allowed login and the session
// cookie, so it is good only for this login, this browser session and this
// run's key, and only until it is TokenLifetime old.
type tokener struct {
	key []byte
}

func newTokener(key []byte) (*tokener, error) {
	if key == nil {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
	}
	if len(key) < 32 {
		return nil, errors.New("web: the token key must be at least 32 bytes")
	}
	return &tokener{key: append([]byte(nil), key...)}, nil
}

func (t *tokener) mac(login, session string, issued int64) []byte {
	m := hmac.New(sha256.New, t.key)
	// Each part is length-prefixed so no two different inputs run together.
	for _, part := range []string{"sherwood anti-forgery v1", login, session, strconv.FormatInt(issued, 10)} {
		m.Write([]byte(strconv.Itoa(len(part)) + ":" + part))
	}
	return m.Sum(nil)
}

func (t *tokener) issue(login, session string, now time.Time) string {
	issued := now.Unix()
	return strconv.FormatInt(issued, 10) + "." + base64.RawURLEncoding.EncodeToString(t.mac(login, session, issued))
}

// valid reports whether token was issued by this tokener for this login and
// session and is not yet stale. The MAC is compared in constant time.
func (t *tokener) valid(token, login, session string, now time.Time) bool {
	ts, sig, ok := strings.Cut(token, ".")
	if !ok || ts == "" || len(ts) > 19 {
		return false
	}
	issued, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || issued < 0 {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	if !hmac.Equal(got, t.mac(login, session, issued)) {
		return false
	}
	at := time.Unix(issued, 0)
	return !at.After(now.Add(tokenSkew)) && now.Sub(at) <= TokenLifetime
}

// newSession returns a random session identifier.
func newSession() string {
	b := make([]byte, sessionBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on supported systems
	return base64.RawURLEncoding.EncodeToString(b)
}

// sessionFrom returns the request's session identifier, if it has a
// well-formed one.
func sessionFrom(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || len(b) != sessionBytes {
		return "", false
	}
	return c.Value, true
}

func sessionCookie(id string) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		MaxAge:   int(sessionMaxAge / time.Second),
		Secure:   true, // Serve gives the phone HTTPS
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}
