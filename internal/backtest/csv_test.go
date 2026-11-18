package backtest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bars below are made up.

func TestReadCSV(t *testing.T) {
	bars, err := ReadCSV("AAA", strings.NewReader("Date,Open,High,Low,Close,Volume\n"+
		"2025-03-03,10.00,10.50,9.90,10.25,1200\n"+
		"2025-03-04, 10.25 ,10.605,10.1049,10.4,\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 || bars[0].Volume != 1200 || bars[1].DateString() != "2025-03-04" {
		t.Fatalf("bars = %+v", bars)
	}
	// Prices past the cent are rounded, halves away from zero.
	if got := bars[1].High.String() + " " + bars[1].Low.String() + " " + bars[1].Close.String(); got != "10.61 10.10 10.40" {
		t.Errorf("rounded prices = %s", got)
	}
	if _, err := ReadCSV("AAA", strings.NewReader("Date,Open,High,Low,Close\n2025-03-03,1,1,1,1\n")); err != nil {
		t.Errorf("a file without volume: %v", err)
	}
}

func TestReadCSVRefuses(t *testing.T) {
	const head = "Date,Open,High,Low,Close\n"
	for name, text := range map[string]string{
		"empty":           "",
		"no rows":         head,
		"wrong header":    "date,open,high,low,close\n2025-03-03,1,1,1,1\n",
		"extra column":    "Date,Open,High,Low,Close,Adj\n2025-03-03,1,1,1,1,1\n",
		"bad date":        head + "03/03/2025,1,1,1,1\n",
		"exponent":        head + "2025-03-03,1e1,10,10,10\n",
		"negative":        head + "2025-03-03,-1,1,1,1\n",
		"short row":       head + "2025-03-03,1,1,1\n",
		"high below low":  head + "2025-03-03,1,1,2,1\n",
		"out of order":    head + "2025-03-04,1,1,1,1\n2025-03-03,1,1,1,1\n",
		"repeated date":   head + "2025-03-03,1,1,1,1\n2025-03-03,1,1,1,1\n",
		"negative volume": "Date,Open,High,Low,Close,Volume\n2025-03-03,1,1,1,1,-5\n",
	} {
		if _, err := ReadCSV("AAA", strings.NewReader(text)); !errors.Is(err, ErrCSV) {
			t.Errorf("%s: err = %v, want ErrCSV", name, err)
		}
	}
}

func TestLoadCSVDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AAA.csv"), []byte("Date,Open,High,Low,Close\n2025-03-03,1,1,1,1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCSVDir(dir, []string{"AAA", "AAA"})
	if err != nil || len(got["AAA"]) != 1 {
		t.Fatalf("got %v, err %v", got, err)
	}
	if _, err := LoadCSVDir(dir, []string{"BBB"}); !errors.Is(err, ErrCSV) {
		t.Errorf("a missing file gave %v", err)
	}
	if _, err := LoadCSVDir(dir, []string{"../AAA"}); err == nil {
		t.Error("a symbol with a path in it was read")
	}
}
