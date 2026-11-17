package money_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/store"
)

func TestParseMoney(t *testing.T) {
	cases := []struct {
		in, want string
		err      error
	}{
		{"0", "0.00", nil},
		{"12.5", "12.50", nil},
		{"12.50", "12.50", nil},
		{"-3", "-3.00", nil},
		{"0.123456", "0.123456", nil},
		{"0.10000", "0.10", nil},
		{"123456789012345678901234567890.123456789", "123456789012345678901234567890.123456789", nil},
		{"", "", money.ErrSyntax},
		{" 1", "", money.ErrSyntax},
		{"+1", "", money.ErrSyntax},
		{".5", "", money.ErrSyntax},
		{"5.", "", money.ErrSyntax},
		{"1e3", "", money.ErrSyntax},
		{"1E-2", "", money.ErrSyntax},
		{"NaN", "", money.ErrSyntax},
		{"Infinity", "", money.ErrSyntax},
		{"0x10", "", money.ErrSyntax},
		{"1,000", "", money.ErrSyntax},
		{"1_000", "", money.ErrSyntax},
		{"1111111111111111111111111111111111111111111111111111111111111111.1", "", money.ErrSyntax},
	}
	for _, c := range cases {
		m, err := money.ParseMoney(c.in)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("ParseMoney(%q) err = %v, want %v", c.in, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMoney(%q): %v", c.in, err)
			continue
		}
		if got := m.String(); got != c.want {
			t.Errorf("ParseMoney(%q).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParsePrice(t *testing.T) {
	cases := []struct {
		in, want string
		err      error
	}{
		{"187.25", "187.25", nil},
		{"3", "3.00", nil},
		{"0.5", "0.50", nil},
		{"0", "0.00", nil},
		{"1.500", "1.50", nil},
		{"1.505", "", money.ErrPrecision},
		{"0.001", "", money.ErrPrecision},
		{"-1.00", "", money.ErrNegative},
		{"1e2", "", money.ErrSyntax},
	}
	for _, c := range cases {
		p, err := money.ParsePrice(c.in)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("ParsePrice(%q) err = %v, want %v", c.in, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePrice(%q): %v", c.in, err)
			continue
		}
		if got := p.String(); got != c.want {
			t.Errorf("ParsePrice(%q).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRoundPrice(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1.004", "1.00"},
		{"1.005", "1.01"},
		{"1.0049999", "1.00"},
		{"2.675", "2.68"}, // 2.675 is 2.67499... in binary floating point; here it is exact
		{"0.001", "0.00"},
		{"177.8875", "177.89"},
		{"10", "10.00"},
	}
	for _, c := range cases {
		p, err := money.RoundPrice(money.MustParseMoney(c.in).Decimal())
		if err != nil {
			t.Errorf("RoundPrice(%s): %v", c.in, err)
			continue
		}
		if got := p.String(); got != c.want {
			t.Errorf("RoundPrice(%s) = %s, want %s", c.in, got, c.want)
		}
	}
	if _, err := money.RoundPrice(money.MustParseMoney("-0.01").Decimal()); !errors.Is(err, money.ErrNegative) {
		t.Errorf("RoundPrice(-0.01) err = %v, want ErrNegative", err)
	}
	// -0.004 rounds to zero, which is a valid price.
	if p, err := money.RoundPrice(money.MustParseMoney("-0.004").Decimal()); err != nil || !p.IsZero() {
		t.Errorf("RoundPrice(-0.004) = %v, %v; want 0.00", p, err)
	}
}

func TestMoneyArithmetic(t *testing.T) {
	m := money.MustParseMoney
	if got := m("0.1").Add(m("0.2")); !got.Equal(m("0.3")) {
		t.Errorf("0.1 + 0.2 = %s, want exactly 0.3", got)
	}
	if got := m("10.00").Sub(m("10.01")); got.String() != "-0.01" || !got.IsNegative() || got.Sign() != -1 {
		t.Errorf("10.00 - 10.01 = %s", got)
	}
	if got := m("5").Neg(); !got.Equal(m("-5")) {
		t.Errorf("Neg(5) = %s", got)
	}
	if got := m("-5.25").Abs(); !got.Equal(m("5.25")) || !got.IsPositive() {
		t.Errorf("Abs(-5.25) = %s", got)
	}
	if !m("1.5").Equal(m("1.50")) || m("1.5").Cmp(m("1.50")) != 0 {
		t.Error("1.5 and 1.50 should be equal")
	}
	if m("1.49").Cmp(m("1.5")) != -1 || m("2").Cmp(m("1.999999")) != 1 {
		t.Error("Cmp ordering wrong")
	}
	if !(money.Money{}).IsZero() || !m("0.000").IsZero() || m("0.000001").IsZero() {
		t.Error("IsZero wrong")
	}
	if got := money.Cents(-1234); got.String() != "-12.34" {
		t.Errorf("Cents(-1234) = %s", got)
	}
	for in, want := range map[string]string{"0.005": "0.01", "-0.005": "-0.01", "1.234": "1.23", "99.995": "100.00"} {
		if got := m(in).RoundCents(); got.String() != want {
			t.Errorf("RoundCents(%s) = %s, want %s", in, got, want)
		}
	}
	if !m("1.10").IsCents() || m("1.101").IsCents() {
		t.Error("IsCents wrong")
	}
	// Adding a cent ten million times stays exact.
	total := money.Money{}
	for range 10_000_000 / 1000 {
		total = total.Add(money.Cents(1000))
	}
	if !total.Equal(m("100000")) {
		t.Errorf("sum = %s, want 100000.00", total)
	}
}

func TestPriceTimesQuantity(t *testing.T) {
	cases := []struct{ price, qty, want string }{
		{"187.25", "3", "561.75"},
		{"0.01", "0.000001", "0.00000001"},
		{"33.33", "3", "99.99"},
		{"100.00", "0", "0.00"},
		{"19.99", "0.333333", "6.66332667"},
	}
	for _, c := range cases {
		got := money.MustParsePrice(c.price).Mul(money.MustParseQuantity(c.qty))
		if got.String() != c.want {
			t.Errorf("%s x %s = %s, want %s", c.price, c.qty, got, c.want)
		}
	}
	p := money.MustParsePrice("12.34")
	if !p.Money().Equal(money.MustParseMoney("12.34")) {
		t.Error("Price.Money wrong")
	}
	if c, err := money.PriceFromCents(1234); err != nil || !c.Equal(p) {
		t.Errorf("PriceFromCents(1234) = %v, %v", c, err)
	}
	if _, err := money.PriceFromCents(-1); !errors.Is(err, money.ErrNegative) {
		t.Errorf("PriceFromCents(-1) err = %v", err)
	}
	if p.Cmp(money.MustParsePrice("12.35")) != -1 || !p.Equal(money.MustParsePrice("12.340")) || p.IsZero() || !(money.Price{}).IsZero() {
		t.Error("Price compare wrong")
	}
}

func TestQuantityPolicy(t *testing.T) {
	if money.PolicyFor(false) != money.WholeShares || money.PolicyFor(true) != money.FractionalShares {
		t.Fatal("PolicyFor wrong")
	}
	parse := []struct {
		policy money.QuantityPolicy
		in     string
		want   string
		err    error
	}{
		{money.WholeShares, "10", "10", nil},
		{money.WholeShares, "10.000", "10", nil},
		{money.WholeShares, "2.5", "", money.ErrPrecision},
		{money.FractionalShares, "2.5", "2.5", nil},
		{money.FractionalShares, "0.000001", "0.000001", nil},
		{money.FractionalShares, "0.0000001", "", money.ErrPrecision},
		{money.WholeShares, "-1", "", money.ErrNegative},
		{money.QuantityPolicy(0), "1", "", money.ErrPolicy},
		{money.QuantityPolicy(9), "1", "", money.ErrPolicy},
	}
	for _, c := range parse {
		q, err := c.policy.Parse(c.in)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("%v.Parse(%q) err = %v, want %v", c.policy, c.in, err, c.err)
			}
			continue
		}
		if err != nil || q.String() != c.want {
			t.Errorf("%v.Parse(%q) = %v, %v; want %s", c.policy, c.in, q, err, c.want)
		}
	}

	round := []struct {
		policy  money.QuantityPolicy
		in      string
		want    string
		wantErr error
	}{
		{money.WholeShares, "7.999", "7", nil},
		{money.WholeShares, "0.9", "0", nil},
		{money.FractionalShares, "1.23456789", "1.234567", nil},
		{money.FractionalShares, "3", "3", nil},
		{money.WholeShares, "-0.5", "", money.ErrNegative},
		{money.QuantityPolicy(0), "1", "", money.ErrPolicy},
	}
	for _, c := range round {
		q, err := c.policy.Round(money.MustParseMoney(c.in).Decimal())
		if c.wantErr != nil {
			if !errors.Is(err, c.wantErr) {
				t.Errorf("%v.Round(%s) err = %v, want %v", c.policy, c.in, err, c.wantErr)
			}
			continue
		}
		if err != nil || q.String() != c.want {
			t.Errorf("%v.Round(%s) = %v, %v; want %s", c.policy, c.in, q, err, c.want)
		}
	}

	sizing := []struct {
		policy        money.QuantityPolicy
		budget, price string
		want          string
		err           error
	}{
		{money.WholeShares, "1000", "187.25", "5", nil},
		{money.FractionalShares, "1000", "187.25", "5.340453", nil},
		{money.WholeShares, "561.75", "187.25", "3", nil},
		{money.WholeShares, "561.74", "187.25", "2", nil},
		{money.WholeShares, "100", "333.33", "0", nil},
		{money.WholeShares, "0", "10", "0", nil},
		{money.WholeShares, "-50", "10", "0", nil},
		{money.WholeShares, "100", "0", "", money.ErrDivideByZero},
		{money.QuantityPolicy(0), "100", "10", "", money.ErrPolicy},
	}
	for _, c := range sizing {
		budget, price := money.MustParseMoney(c.budget), money.MustParsePrice(c.price)
		q, err := c.policy.SharesFor(budget, price)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("%v.SharesFor(%s, %s) err = %v, want %v", c.policy, c.budget, c.price, err, c.err)
			}
			continue
		}
		if err != nil || q.String() != c.want {
			t.Errorf("%v.SharesFor(%s, %s) = %v, %v; want %s", c.policy, c.budget, c.price, q, err, c.want)
			continue
		}
		// Never over budget, and one more step would be.
		if cost := price.Mul(q); cost.Cmp(budget) > 0 && budget.IsPositive() {
			t.Errorf("SharesFor(%s, %s) = %s costs %s, over budget", c.budget, c.price, q, cost)
		}
		if err := c.policy.Check(q); err != nil {
			t.Errorf("SharesFor result %s fails its own policy: %v", q, err)
		}
	}
}

func TestQuantityArithmetic(t *testing.T) {
	q := money.MustParseQuantity
	if got := q("1.5").Add(q("2.25")); !got.Equal(q("3.75")) {
		t.Errorf("Add = %s", got)
	}
	if got, err := q("10").Sub(q("4.5")); err != nil || got.String() != "5.5" {
		t.Errorf("Sub = %s, %v", got, err)
	}
	if _, err := q("1").Sub(q("1.000001")); !errors.Is(err, money.ErrNegative) {
		t.Errorf("Sub below zero err = %v", err)
	}
	if !q("2").IsWhole() || q("2.1").IsWhole() || !q("0").IsZero() || q("1").Cmp(q("0.999999")) != 1 {
		t.Error("Quantity predicates wrong")
	}
	if s, err := money.Shares(12); err != nil || s.String() != "12" {
		t.Errorf("Shares(12) = %v, %v", s, err)
	}
	if _, err := money.Shares(-1); !errors.Is(err, money.ErrNegative) {
		t.Errorf("Shares(-1) err = %v", err)
	}
	if _, err := money.ParseQuantity("0.0000001"); !errors.Is(err, money.ErrPrecision) {
		t.Errorf("seven places err = %v", err)
	}
}

// row has one field of each type, plus optional ones.
type row struct {
	Money    money.Money     `json:"money"`
	Price    money.Price     `json:"price"`
	Quantity money.Quantity  `json:"quantity"`
	Stop     *money.Price    `json:"stop"`
	Cash     *money.Money    `json:"cash,omitempty"`
	Held     *money.Quantity `json:"held"`
}

var roundTrip = []struct{ money, price, quantity string }{
	{"0", "0", "0"},
	{"0.1", "0.10", "1"},
	{"-1234.56", "187.25", "3"},
	{"0.00000001", "0.01", "0.000001"},
	{"99999999999999999999.99", "99999999.99", "123456789.123456"},
	{"-0.000001", "1.5", "2.5"},
	{"123456789012345678901234567890.123456789", "4.00", "1000000"},
}

func TestJSONRoundTrip(t *testing.T) {
	for _, c := range roundTrip {
		stop := money.MustParsePrice(c.price)
		in := row{
			Money:    money.MustParseMoney(c.money),
			Price:    money.MustParsePrice(c.price),
			Quantity: money.MustParseQuantity(c.quantity),
			Stop:     &stop,
		}
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out row
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("Unmarshal(%s): %v", b, err)
		}
		if !out.Money.Equal(in.Money) || !out.Price.Equal(in.Price) || !out.Quantity.Equal(in.Quantity) ||
			out.Stop == nil || !out.Stop.Equal(stop) || out.Cash != nil || out.Held != nil {
			t.Errorf("JSON round trip of %s gave %+v", b, out)
		}
		// Every value travels as a string.
		var raw map[string]any
		if err := json.Unmarshal(b, &raw); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"money", "price", "quantity", "stop"} {
			if _, ok := raw[k].(string); !ok {
				t.Errorf("%s encoded as %T in %s, want a string", k, raw[k], b)
			}
		}
	}
	b, err := json.Marshal(row{Price: money.MustParsePrice("3"), Money: money.MustParseMoney("12.5"), Quantity: money.MustParseQuantity("2")})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"money":"12.50","price":"3.00","quantity":"2","stop":null,"held":null}`; string(b) != want {
		t.Errorf("Marshal = %s, want %s", b, want)
	}
}

func TestJSONRefusesNumbers(t *testing.T) {
	bad := []struct {
		name, doc string
		err       error
	}{
		{"money number", `{"money": 12.5}`, money.ErrNotText},
		{"money integer", `{"money": 12}`, money.ErrNotText},
		{"price number", `{"price": 187.25}`, money.ErrNotText},
		{"quantity number", `{"quantity": 3}`, money.ErrNotText},
		{"optional price number", `{"stop": 1.5}`, money.ErrNotText},
		{"money null", `{"money": null}`, money.ErrNotText},
		{"money bool", `{"money": true}`, money.ErrNotText},
		{"money exponent string", `{"money": "1e3"}`, money.ErrSyntax},
		{"money escaped string", `{"money": "1` + "\\" + `u002e5"}`, money.ErrSyntax},
		{"price sub-cent", `{"price": "1.005"}`, money.ErrPrecision},
		{"price negative", `{"price": "-1"}`, money.ErrNegative},
		{"quantity negative", `{"quantity": "-1"}`, money.ErrNegative},
	}
	for _, c := range bad {
		var r row
		err := json.Unmarshal([]byte(c.doc), &r)
		if err == nil {
			t.Errorf("%s: %s decoded to %+v, want an error", c.name, c.doc, r)
			continue
		}
		if !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.err)
		}
	}
	// A JSON number is refused even through a json.Decoder with UseNumber.
	d := json.NewDecoder(strings.NewReader(`{"money": 1.5}`))
	d.UseNumber()
	var r row
	if err := d.Decode(&r); !errors.Is(err, money.ErrNotText) {
		t.Errorf("UseNumber decode err = %v", err)
	}
	// null into an optional field is fine and leaves it nil.
	if err := json.Unmarshal([]byte(`{"stop": null, "cash": null}`), &r); err != nil || r.Stop != nil || r.Cash != nil {
		t.Errorf("null optional: %v, %+v", err, r)
	}
}

func TestTextRoundTrip(t *testing.T) {
	for _, c := range roundTrip {
		m := money.MustParseMoney(c.money)
		b, _ := m.MarshalText()
		var m2 money.Money
		if err := m2.UnmarshalText(b); err != nil || !m2.Equal(m) {
			t.Errorf("Money text %s: %v", b, err)
		}
		p := money.MustParsePrice(c.price)
		b, _ = p.MarshalText()
		var p2 money.Price
		if err := p2.UnmarshalText(b); err != nil || !p2.Equal(p) {
			t.Errorf("Price text %s: %v", b, err)
		}
		q := money.MustParseQuantity(c.quantity)
		b, _ = q.MarshalText()
		var q2 money.Quantity
		if err := q2.UnmarshalText(b); err != nil || !q2.Equal(q) {
			t.Errorf("Quantity text %s: %v", b, err)
		}
	}
}

func TestScanRefusesNonText(t *testing.T) {
	type scanner interface{ Scan(any) error }
	for _, dst := range []scanner{&money.Money{}, &money.Price{}, &money.Quantity{}} {
		for _, src := range []any{1.5, float32(1.5), int64(2), nil, true, time.Time{}} {
			if err := dst.Scan(src); !errors.Is(err, money.ErrNotText) {
				t.Errorf("%T.Scan(%T) err = %v, want ErrNotText", dst, src, err)
			}
		}
		if err := dst.Scan("1.5"); err != nil {
			t.Errorf("%T.Scan(string): %v", dst, err)
		}
		if err := dst.Scan([]byte("2")); err != nil {
			t.Errorf("%T.Scan([]byte): %v", dst, err)
		}
		if err := dst.Scan("1e2"); !errors.Is(err, money.ErrSyntax) {
			t.Errorf("%T.Scan(1e2) err = %v", dst, err)
		}
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "money.db"), time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.Writer().ExecContext(ctx, `CREATE TABLE amounts (
		id       INTEGER PRIMARY KEY,
		money    TEXT NOT NULL,
		price    TEXT NOT NULL,
		quantity TEXT NOT NULL,
		stop     TEXT
	) STRICT`)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSQLiteRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	for i, c := range roundTrip {
		m, p, q := money.MustParseMoney(c.money), money.MustParsePrice(c.price), money.MustParseQuantity(c.quantity)
		var stop any // NULL on even rows
		if i%2 == 1 {
			stop = p
		}
		if _, err := s.Writer().ExecContext(ctx,
			`INSERT INTO amounts (id, money, price, quantity, stop) VALUES (?, ?, ?, ?, ?)`, i, m, p, q, stop); err != nil {
			t.Fatalf("insert %v: %v", c, err)
		}

		var (
			gm    money.Money
			gp    money.Price
			gq    money.Quantity
			gstop sql.Null[money.Price]
			types string
		)
		err := s.Reader().QueryRowContext(ctx,
			`SELECT money, price, quantity, stop, typeof(money) || typeof(price) || typeof(quantity) FROM amounts WHERE id = ?`, i).
			Scan(&gm, &gp, &gq, &gstop, &types)
		if err != nil {
			t.Fatalf("select %v: %v", c, err)
		}
		if !gm.Equal(m) || !gp.Equal(p) || !gq.Equal(q) {
			t.Errorf("SQLite round trip %v gave %s %s %s", c, gm, gp, gq)
		}
		if gm.String() != m.String() || gp.String() != p.String() || gq.String() != q.String() {
			t.Errorf("SQLite round trip text changed for %v", c)
		}
		if types != "texttexttext" {
			t.Errorf("stored as %s, want text", types)
		}
		if gstop.Valid != (i%2 == 1) || (gstop.Valid && !gstop.V.Equal(p)) {
			t.Errorf("nullable stop = %+v", gstop)
		}
	}
}

func TestSQLiteRefusesReal(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	queries := []string{
		`SELECT 1.5`,
		`SELECT CAST('187.25' AS REAL)`,
		`SELECT 2`,
		`SELECT NULL`,
	}
	for _, q := range queries {
		var m money.Money
		if err := s.Reader().QueryRowContext(ctx, q).Scan(&m); !errors.Is(err, money.ErrNotText) {
			t.Errorf("%s into Money: err = %v, want ErrNotText", q, err)
		}
		var p money.Price
		if err := s.Reader().QueryRowContext(ctx, q).Scan(&p); !errors.Is(err, money.ErrNotText) {
			t.Errorf("%s into Price: err = %v, want ErrNotText", q, err)
		}
		var n money.Quantity
		if err := s.Reader().QueryRowContext(ctx, q).Scan(&n); !errors.Is(err, money.ErrNotText) {
			t.Errorf("%s into Quantity: err = %v, want ErrNotText", q, err)
		}
	}
}
