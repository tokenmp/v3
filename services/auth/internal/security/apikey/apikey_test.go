package apikey

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestGenerate_LengthEntropyAndHash(t *testing.T) {
	key1, hash1, err := Generate()
	if err != nil {
		t.Fatalf("Generate first: %v", err)
	}
	key2, hash2, err := Generate()
	if err != nil {
		t.Fatalf("Generate second: %v", err)
	}
	if key1 == key2 {
		t.Fatal("two generated keys were equal")
	}
	for _, key := range []string{key1, key2} {
		if !strings.HasPrefix(key, PrefixMarker) {
			t.Errorf("key %q missing prefix %q", key, PrefixMarker)
		}
		payload := strings.TrimPrefix(key, PrefixMarker)
		if strings.Contains(payload, "=") {
			t.Errorf("key payload has base64 padding: %q", payload)
		}
		raw, err := base64.RawURLEncoding.DecodeString(payload)
		if err != nil {
			t.Errorf("key payload is not base64url: %v", err)
		}
		if len(raw) != TokenLength {
			t.Errorf("decoded length = %d, want %d", len(raw), TokenLength)
		}
	}
	if len(hash1) != sha256.Size || len(hash2) != sha256.Size {
		t.Errorf("hash lengths = %d / %d, want %d", len(hash1), len(hash2), sha256.Size)
	}
	if bytes.Equal(hash1, hash2) {
		t.Error("distinct keys produced the same hash")
	}
}

func TestHash_DeterministicAndMatchesGenerate(t *testing.T) {
	key, generatedHash, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	hash1, err := Hash(key)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	hash2, err := Hash(key)
	if err != nil {
		t.Fatalf("Hash second: %v", err)
	}
	if !bytes.Equal(hash1, generatedHash) || !bytes.Equal(hash1, hash2) {
		t.Error("Hash was not deterministic or did not match Generate")
	}
}

func TestPrefixAndSuffix(t *testing.T) {
	key, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got, want := Prefix(key), key[:12]; got != want {
		t.Errorf("Prefix() = %q, want %q", got, want)
	}
	if got, want := Suffix(key), key[len(key)-4:]; got != want {
		t.Errorf("Suffix() = %q, want %q", got, want)
	}
	if got := Prefix("short"); got != "short" {
		t.Errorf("Prefix short = %q", got)
	}
	if got := Suffix("end"); got != "end" {
		t.Errorf("Suffix short = %q", got)
	}
}

func TestHash_Malformed(t *testing.T) {
	wrongLength := PrefixMarker + base64.RawURLEncoding.EncodeToString([]byte("short"))
	for _, key := range []string{"", "other_key", PrefixMarker, PrefixMarker + "not!!!base64", wrongLength} {
		t.Run(key, func(t *testing.T) {
			_, err := Hash(key)
			if !errors.Is(err, ErrMalformedKey) {
				t.Errorf("Hash(%q) error = %v, want ErrMalformedKey", key, err)
			}
		})
	}
}
