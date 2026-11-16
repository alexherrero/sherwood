package secret_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/secret"
)

// fakeValue is an obviously fake secret. Every test checks that it never
// appears in output.
const fakeValue = "fake-test-secret-ABC123"

func assertMasked(t *testing.T, what, out string) {
	t.Helper()
	if strings.Contains(out, fakeValue) {
		t.Errorf("%s leaked the secret: %q", what, out)
	}
	for _, part := range []string{"fake", "ABC123", "414243"} { // 414243 is "ABC" in hex
		if strings.Contains(out, part) {
			t.Errorf("%s leaked part of the secret (%q): %q", what, part, out)
		}
	}
	if !strings.Contains(out, secret.Mask) {
		t.Errorf("%s shows no mask: %q", what, out)
	}
}

func TestPrintAndFormatShowOnlyTheMask(t *testing.T) {
	s := secret.New(fakeValue)
	verbs := []string{
		"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d",
		"%10s", "%-10v", "%.3s", "% x", "%#x", "%+q",
	}
	for _, verb := range verbs {
		assertMasked(t, "Sprintf "+verb, fmt.Sprintf(verb, s))
		assertMasked(t, "Sprintf "+verb+" of a pointer", fmt.Sprintf(verb, &s))
	}
	assertMasked(t, "Sprint", fmt.Sprint(s))
	assertMasked(t, "Sprintln", fmt.Sprintln(s))
	assertMasked(t, "String", s.String())
	assertMasked(t, "GoString", s.GoString())
	assertMasked(t, "error wrapping", fmt.Errorf("login failed with %v", s).Error())

	var buf bytes.Buffer
	l := log.New(&buf, "", 0)
	l.Print(s)
	l.Printf("token=%s", s)
	assertMasked(t, "log.Print", buf.String())
}

// settingsLike mirrors how a settings struct holds secrets: exported and
// unexported fields, by value and by pointer, in a map and a slice.
type settingsLike struct {
	Name    string
	Token   secret.Secret
	Pointer *secret.Secret
	ByName  map[string]secret.Secret
	List    []secret.Secret
	hidden  secret.Secret
}

func TestSecretInsideAStructShowsOnlyTheMask(t *testing.T) {
	p := secret.New(fakeValue)
	v := settingsLike{
		Name:    "example",
		Token:   secret.New(fakeValue),
		Pointer: &p,
		ByName:  map[string]secret.Secret{"feed": secret.New(fakeValue)},
		List:    []secret.Secret{secret.New(fakeValue)},
		hidden:  secret.New(fakeValue),
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%q"} {
		out := fmt.Sprintf(verb, v)
		if strings.Contains(out, fakeValue) || strings.Contains(out, "ABC123") || strings.Contains(out, "414243") {
			t.Errorf("Sprintf %s of a struct leaked the secret: %q", verb, out)
		}
		out = fmt.Sprintf(verb, &v)
		if strings.Contains(out, fakeValue) || strings.Contains(out, "ABC123") || strings.Contains(out, "414243") {
			t.Errorf("Sprintf %s of a struct pointer leaked the secret: %q", verb, out)
		}
	}
	_ = v.hidden // read so the field is used
}

func TestJSONAndTextEncodingShowOnlyTheMask(t *testing.T) {
	s := secret.New(fakeValue)

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	assertMasked(t, "json.Marshal", string(b))
	if string(b) != `"`+secret.Mask+`"` {
		t.Errorf("json.Marshal = %s, want the quoted mask", b)
	}

	b, err = json.Marshal(settingsLike{Token: s, Pointer: &s, ByName: map[string]secret.Secret{"k": s}, List: []secret.Secret{s}})
	if err != nil {
		t.Fatal(err)
	}
	assertMasked(t, "json.Marshal of a struct", string(b))

	b, err = json.MarshalIndent(map[string]any{"token": s}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	assertMasked(t, "json.MarshalIndent of a map", string(b))

	b, err = s.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	assertMasked(t, "MarshalText", string(b))

	y, err := s.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	assertMasked(t, "MarshalYAML", fmt.Sprint(y))
}

func TestLoggingShowsOnlyTheMask(t *testing.T) {
	s := secret.New(fakeValue)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("x", "token", s, "pointer", &s)
	logger.Info("y", slog.Any("token", s))
	assertMasked(t, "slog JSON", buf.String())

	buf.Reset()
	logger = slog.New(slog.NewTextHandler(&buf, nil))
	logger.Info("x", "token", s, "pointer", &s, "struct", settingsLike{Token: s})
	assertMasked(t, "slog text", buf.String())
}

func TestRevealReturnsTheRealValue(t *testing.T) {
	s := secret.New(fakeValue)
	if got := s.Reveal(); got != fakeValue {
		t.Errorf("Reveal = %q, want the value passed to New", got)
	}
	var zero secret.Secret
	if zero.Reveal() != "" || !zero.IsZero() {
		t.Error("the zero Secret should be empty")
	}
	if s.IsZero() {
		t.Error("a set Secret should not be zero")
	}
	if !secret.New("").IsZero() {
		t.Error("New(\"\") should be zero")
	}
}

func TestZeroValueStillShowsTheMask(t *testing.T) {
	var zero secret.Secret
	if got := fmt.Sprint(zero); got != secret.Mask {
		t.Errorf("Sprint of the zero Secret = %q, want the mask", got)
	}
}

func TestMatches(t *testing.T) {
	s := secret.New(fakeValue)
	if !s.Matches(fakeValue) {
		t.Error("Matches should accept the same value")
	}
	for _, c := range []string{"", "fake", fakeValue + "x", strings.ToUpper(fakeValue)} {
		if s.Matches(c) {
			t.Errorf("Matches(%q) should be false", c)
		}
	}
	var zero secret.Secret
	if zero.Matches("") {
		t.Error("an empty secret should match nothing, not even the empty string")
	}
}

func TestUnmarshalJSON(t *testing.T) {
	var v struct {
		Token secret.Secret  `json:"token"`
		Other *secret.Secret `json:"other"`
		Null  secret.Secret  `json:"null"`
	}
	in := `{"token": "` + fakeValue + `", "other": "second-fake", "null": null}`
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if v.Token.Reveal() != fakeValue {
		t.Errorf("token = %q", v.Token.Reveal())
	}
	if v.Other == nil || v.Other.Reveal() != "second-fake" {
		t.Errorf("other was not read")
	}
	if !v.Null.IsZero() {
		t.Error("null should give an empty secret")
	}

	// A wrong type is refused with an error that does not echo the input.
	for _, bad := range []string{`{"token": 12345678}`, `{"token": {"inner": "fake-ABC123"}}`, `{"token": ["fake-ABC123"]}`} {
		err := json.Unmarshal([]byte(bad), &v)
		if err == nil {
			t.Errorf("Unmarshal(%s) should fail", bad)
			continue
		}
		if strings.Contains(err.Error(), "ABC123") || strings.Contains(err.Error(), "12345678") {
			t.Errorf("the error echoes the input: %v", err)
		}
	}
}

func TestUnmarshalText(t *testing.T) {
	var s secret.Secret
	if err := s.UnmarshalText([]byte(fakeValue)); err != nil {
		t.Fatal(err)
	}
	if s.Reveal() != fakeValue {
		t.Errorf("UnmarshalText read %q", s.Reveal())
	}
}

// fakeYAMLUnmarshal stands in for a YAML library calling the
// UnmarshalYAML(func(any) error) form: it stores node into the target, or
// fails the way a library does on a type mismatch.
func fakeYAMLUnmarshal(node any) func(any) error {
	return func(target any) error {
		p, ok := target.(*string)
		if !ok {
			return errors.New("unexpected target type")
		}
		s, ok := node.(string)
		if !ok {
			return fmt.Errorf("yaml: cannot unmarshal %v into string", node)
		}
		*p = s
		return nil
	}
}

func TestUnmarshalYAML(t *testing.T) {
	var s secret.Secret
	if err := s.UnmarshalYAML(fakeYAMLUnmarshal(fakeValue)); err != nil {
		t.Fatal(err)
	}
	if s.Reveal() != fakeValue {
		t.Errorf("UnmarshalYAML read %q", s.Reveal())
	}

	err := s.UnmarshalYAML(fakeYAMLUnmarshal(map[string]string{"k": "fake-ABC123"}))
	if err == nil {
		t.Fatal("a mapping should be refused")
	}
	if strings.Contains(err.Error(), "ABC123") {
		t.Errorf("the error echoes the input: %v", err)
	}
}
