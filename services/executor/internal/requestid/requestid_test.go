package requestid

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestValidReservationIDGrammar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"empty", "", false},
		{"prefix only", "res_", false},
		{"no prefix", strings.Repeat("a", 22), false},
		{"short suffix", "res_short", false},
		{"exactly 16 suffix", "res_" + strings.Repeat("a", 16), true},
		{"exactly 128 suffix", "res_" + strings.Repeat("a", 128), true},
		{"129 suffix too long", "res_" + strings.Repeat("a", 129), false},
		{"default csprng shape", "res_" + strings.Repeat("A", 22), true},
		{"uppercase allowed", "res_" + strings.Repeat("Z", 16), true},
		{"digits allowed", "res_" + strings.Repeat("0", 16), true},
		{"dash allowed", "res_" + strings.Repeat("-", 16), true},
		{"underscore allowed", "res_" + strings.Repeat("_", 16), true},
		{"plus rejected", "res_" + strings.Repeat("+", 16), false},
		{"slash rejected", "res_" + strings.Repeat("/", 16), false},
		{"equals rejected", "res_" + strings.Repeat("=", 16), false},
		{"space rejected", "res_" + strings.Repeat(" ", 16), false},
		{"newline rejected", "res_" + "aaaaaaaaaaaaaaaa\n", false},
		{"dot rejected", "res_" + strings.Repeat(".", 16), false},
		{"unicode rejected", "res_" + strings.Repeat("é", 16), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidReservationID(tc.id); got != tc.want {
				t.Fatalf("ValidReservationID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestRandomDefaultShapeAndGrammar(t *testing.T) {
	t.Parallel()
	id := Random{}.ReservationID(context.Background())
	if id == "" {
		t.Fatal("default reservation id empty")
	}
	if !ValidReservationID(id) {
		t.Fatalf("default reservation id %q failed grammar", id)
	}
	// 16 random bytes RawURLEncoding => 22 chars, total len "res_"+22 = 26.
	if len(id) != len(ReservationPrefix)+22 {
		t.Fatalf("default len = %d, want %d", len(id), len(ReservationPrefix)+22)
	}
	// Two draws must differ (probabilistically; 128 bits makes collision absurd).
	other := Random{}.ReservationID(context.Background())
	if id == other {
		t.Fatal("two random reservation ids collided")
	}
}

func TestRandomDeterministicReader(t *testing.T) {
	t.Parallel()
	seed := bytes.Repeat([]byte{0xAB}, RandomBytes)
	id := Random{Reader: bytes.NewReader(seed)}.ReservationID(context.Background())
	want := "res_" + base64.RawURLEncoding.EncodeToString(seed)
	if id != want {
		t.Fatalf("id = %q, want %q", id, want)
	}
	if !ValidReservationID(id) {
		t.Fatalf("deterministic id %q failed grammar", id)
	}
}

func TestRandomShortReadFailsClosed(t *testing.T) {
	t.Parallel()
	// A reader returning fewer than RandomBytes bytes is a short read and must
	// yield an empty (fail-closed) identifier, never a truncated one.
	short := bytes.Repeat([]byte{0x01}, RandomBytes-1)
	id := Random{Reader: bytes.NewReader(short)}.ReservationID(context.Background())
	if id != "" {
		t.Fatalf("short read produced non-empty id %q", id)
	}
}

func TestRandomReaderErrorFailsClosed(t *testing.T) {
	t.Parallel()
	id := Random{Reader: errReader{}}.ReservationID(context.Background())
	if id != "" {
		t.Fatalf("error reader produced non-empty id %q", id)
	}
}

func TestRandomZeroReaderUsesCryptoRand(t *testing.T) {
	t.Parallel()
	var r Random // zero value: Reader == nil
	id := r.ReservationID(context.Background())
	if id == "" {
		t.Fatal("zero-value Random returned empty")
	}
	if !ValidReservationID(id) {
		t.Fatalf("zero-value id %q failed grammar", id)
	}
}

func TestSourceFunc(t *testing.T) {
	t.Parallel()
	src := SourceFunc(func(context.Context) string { return "injected" })
	if got := src.ReservationID(context.Background()); got != "injected" {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultSourceProducesValidIDs(t *testing.T) {
	t.Parallel()
	id := Default.ReservationID(context.Background())
	if !ValidReservationID(id) {
		t.Fatalf("Default id %q failed grammar", id)
	}
}

// errReader always errors.
type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }

var _ = errors.New // keep import for future diagnostics if needed
