// Package webpushtest holds test doubles for web push: a push service that
// records what it is sent and never touches the network, and a device that
// subscribes with its own keys and decrypts what reaches it, so a test can
// read exactly what a phone would receive.
package webpushtest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
)

// Request is one push request the fake service received.
type Request struct {
	Endpoint string
	Header   http.Header
	Body     []byte
}

// Service is a fake push service. It implements webpush.HTTPClient, records
// every request and answers with Status (201 when zero), or with the status
// set for one endpoint by Answer. It never opens a connection.
type Service struct {
	mu       sync.Mutex
	requests []Request
	answers  map[string]int
	// Status is the answer for an endpoint with none set by Answer.
	Status int
	// Err, when set, is returned instead of a response, as a network
	// failure would be.
	Err error
}

// Answer makes the service answer requests to endpoint with status.
func (s *Service) Answer(endpoint string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.answers == nil {
		s.answers = map[string]int{}
	}
	s.answers[endpoint] = status
}

// Do records r and answers it.
func (s *Service) Do(r *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()
	endpoint := r.URL.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{Endpoint: endpoint, Header: r.Header.Clone(), Body: body})
	if s.Err != nil {
		return nil, s.Err
	}
	status, ok := s.answers[endpoint]
	if !ok {
		status = s.Status
	}
	if status == 0 {
		status = http.StatusCreated
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Request:    r,
	}, nil
}

// Requests returns every request received so far, oldest first.
func (s *Service) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Device is a browser's push subscription with the private half of its
// keys, so a test can decrypt what was sent to it.
type Device struct {
	Endpoint string
	key      *ecdh.PrivateKey
	auth     [16]byte
}

// NewDevice makes a device whose endpoint is endpoint.
func NewDevice(t testing.TB, endpoint string) *Device {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := &Device{Endpoint: endpoint, key: key}
	if _, err := rand.Read(d.auth[:]); err != nil {
		t.Fatal(err)
	}
	return d
}

// P256dh is the device's public key as the browser's getKey gives it.
func (d *Device) P256dh() string {
	return base64.RawURLEncoding.EncodeToString(d.key.PublicKey().Bytes())
}

// Auth is the device's authentication secret as the browser gives it.
func (d *Device) Auth() string { return base64.RawURLEncoding.EncodeToString(d.auth[:]) }

// Decrypt opens an aes128gcm push body sent to this device (RFC 8291 and
// RFC 8188, one record) and returns the plaintext the phone would read.
func (d *Device) Decrypt(body []byte) ([]byte, error) {
	const head = 16 + 4 + 1
	if len(body) < head {
		return nil, errors.New("webpushtest: body too short")
	}
	salt := body[:16]
	rs := int64(binary.BigEndian.Uint32(body[16:20]))
	idLen := int(body[20])
	if len(body) < head+idLen || int64(len(body)-head-idLen) > rs {
		return nil, errors.New("webpushtest: bad record header")
	}
	serverPub, err := ecdh.P256().NewPublicKey(body[head : head+idLen])
	if err != nil {
		return nil, err
	}
	ciphertext := body[head+idLen:]
	shared, err := d.key.ECDH(serverPub)
	if err != nil {
		return nil, err
	}
	info := append([]byte("WebPush: info\x00"), d.key.PublicKey().Bytes()...)
	info = append(info, serverPub.Bytes()...)
	ikm, err := hkdf.Key(sha256.New, shared, d.auth[:], string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	// The last record ends with 0x02 followed by zero padding.
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		return nil, errors.New("webpushtest: missing padding delimiter")
	}
	return plain[:len(plain)-1], nil
}

// VAPIDKeys makes a fresh VAPID key pair, base64url without padding, in the
// form the settings file holds.
func VAPIDKeys(t testing.TB) (public, private string) {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(k.Bytes())
}
