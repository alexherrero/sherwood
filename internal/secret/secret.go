package secret

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
)

// Mask is what every printed, formatted, logged or encoded form of a Secret
// shows, whatever the real value is and whether or not one is set.
const Mask = "********"

// Secret holds a value that must never be printed, logged or written out by
// mistake. Its String, GoString, Format, MarshalJSON, MarshalText,
// MarshalYAML and LogValue forms are all Mask. The only way to read the real
// value is Reveal, so every use of it can be found with a search.
//
// A Secret can be read straight from a settings file: it implements
// json.Unmarshaler, encoding.TextUnmarshaler and the
// UnmarshalYAML(func(any) error) form that YAML libraries accept.
//
// The zero value is an empty secret. Secrets cannot be compared with ==; use
// IsZero or Matches.
type Secret struct {
	// value is a pointer so that printing a struct that holds a Secret in an
	// unexported field, where fmt cannot call Format, shows an address
	// rather than the text.
	value *string

	// This zero-size field makes Secret incomparable, so == cannot silently
	// compare pointers.
	_ [0]func()
}

// New wraps value in a Secret.
func New(value string) Secret {
	return Secret{value: &value}
}

// Reveal returns the real value. Call it only at the point the value is
// handed to the thing that needs it, and never pass the result to a logger,
// an error or a formatted string.
func (s Secret) Reveal() string {
	if s.value == nil {
		return ""
	}
	return *s.value
}

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool {
	return s.Reveal() == ""
}

// Matches reports whether candidate equals the secret, in time that does not
// depend on where the two differ or on their lengths. An empty secret matches
// nothing.
func (s Secret) Matches(candidate string) bool {
	if s.IsZero() {
		return false
	}
	want := sha256.Sum256([]byte(s.Reveal()))
	got := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

// String returns Mask.
func (s Secret) String() string { return Mask }

// GoString returns Mask, so %#v shows it too.
func (s Secret) GoString() string { return Mask }

// Format writes Mask for every verb and flag, including %v, %+v, %#v, %s,
// %q, %x and %d.
func (s Secret) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(Mask))
}

// LogValue returns Mask, so log/slog never sees the real value.
func (s Secret) LogValue() slog.Value { return slog.StringValue(Mask) }

// MarshalJSON returns Mask as a JSON string.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(Mask) }

// MarshalText returns Mask.
func (s Secret) MarshalText() ([]byte, error) { return []byte(Mask), nil }

// MarshalYAML returns Mask, so writing settings back out never writes the
// real value.
func (s Secret) MarshalYAML() (any, error) { return Mask, nil }

// errNotString is returned when a settings file holds something other than a
// string where a secret belongs. It never quotes the input, which may be the
// secret itself.
var errNotString = errors.New("secret: value must be a string")

// UnmarshalJSON reads a JSON string into the secret. JSON null leaves it
// empty.
func (s *Secret) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = Secret{}
		return nil
	}
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return errNotString
	}
	*s = New(v)
	return nil
}

// UnmarshalText reads text into the secret as it is.
func (s *Secret) UnmarshalText(text []byte) error {
	*s = New(string(text))
	return nil
}

// UnmarshalYAML reads a YAML scalar into the secret. It uses the
// function-argument form, which gopkg.in/yaml.v2, gopkg.in/yaml.v3 and
// github.com/goccy/go-yaml all accept, so this package needs no YAML
// dependency.
func (s *Secret) UnmarshalYAML(unmarshal func(any) error) error {
	var v string
	if err := unmarshal(&v); err != nil {
		return errNotString
	}
	*s = New(v)
	return nil
}
