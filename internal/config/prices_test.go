package config

import (
	"strings"
	"testing"
)

func TestPriceKeysLoad(t *testing.T) {
	data := validSettings + `
prices:
  tiingo_token: "example-tiingo-token"
  stooq_api_key: "example-stooq-key"
`
	s, err := ParseSettings([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if s.Prices.TiingoToken.Reveal() != "example-tiingo-token" || s.Prices.StooqAPIKey.Reveal() != "example-stooq-key" {
		t.Error("price keys did not load")
	}
}

func TestPriceKeysOptional(t *testing.T) {
	s, err := ParseSettings([]byte(validSettings))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Prices.TiingoToken.IsZero() || !s.Prices.StooqAPIKey.IsZero() {
		t.Error("absent price keys should be zero")
	}
}

func TestPriceKeyBadTypeNotEchoed(t *testing.T) {
	data := validSettings + `
prices:
  tiingo_token: [planted-value-123]
`
	_, err := ParseSettings([]byte(data))
	if err == nil {
		t.Fatal("a list for a token should be refused")
	}
	if strings.Contains(err.Error(), "planted-value-123") {
		t.Errorf("error repeats the value: %v", err)
	}
}
