package refresh

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestGenerate_LengthAndEntropy(t *testing.T) {
	tok1, raw1, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	tok2, raw2, err := Generate()
	if err != nil {
		t.Fatalf("Generate second: %v", err)
	}
	if len(raw1) != TokenLength || len(raw2) != TokenLength {
		t.Errorf("raw length = %d / %d, want %d", len(raw1), len(raw2), TokenLength)
	}
	if tok1 == tok2 {
		t.Error("two generated tokens were equal — entropy failure")
	}
	// base64url no padding: no '=' and only url-safe alphabet.
	for _, tok := range []string{tok1, tok2} {
		if strings.Contains(tok, "=") {
			t.Errorf("token has padding: %q", tok)
		}
		if _, err := base64.RawURLEncoding.DecodeString(tok); err != nil {
			t.Errorf("token not valid base64url: %v", err)
		}
	}
}

func TestHash_Deterministic(t *testing.T) {
	_, raw, _ := Generate()
	h1 := Hash(raw)
	h2 := Hash(raw)
	if len(h1) != sha256.Size {
		t.Errorf("hash length = %d, want %d", len(h1), sha256.Size)
	}
	if string(h1) != string(h2) {
		t.Error("hash not deterministic")
	}
}

func TestHashToken_MatchesHash(t *testing.T) {
	tok, raw, _ := Generate()
	ht, err := HashToken(tok)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}
	if string(ht) != string(Hash(raw)) {
		t.Error("HashToken did not equal Hash(raw)")
	}
}

func TestHashToken_DistinctTokens(t *testing.T) {
	tok1, _, _ := Generate()
	tok2, _, _ := Generate()
	h1, _ := HashToken(tok1)
	h2, _ := HashToken(tok2)
	if string(h1) == string(h2) {
		t.Error("two distinct tokens hashed to the same value")
	}
}

func TestHashToken_MalformedString(t *testing.T) {
	// A malformed base64url string must return an error, not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("HashToken panicked on malformed input: %v", r)
		}
	}()
	_, err := HashToken("not!!!valid-b64url")
	if err == nil {
		t.Error("expected error for malformed token, got nil")
	}
	if !errors.Is(err, ErrMalformedToken) {
		t.Errorf("err=%v want ErrMalformedToken", err)
	}
}

func TestHashToken_EmptyString(t *testing.T) {
	_, err := HashToken("")
	if err == nil {
		t.Error("expected error for empty token, got nil")
	}
	if !errors.Is(err, ErrMalformedToken) {
		t.Errorf("err=%v want ErrMalformedToken", err)
	}
}

func TestHashToken_WrongLength(t *testing.T) {
	// A valid base64url string that decodes to != 32 bytes must be rejected.
	short := base64.RawURLEncoding.EncodeToString([]byte("short"))
	_, err := HashToken(short)
	if err == nil {
		t.Error("expected error for wrong-length token, got nil")
	}
	if !errors.Is(err, ErrMalformedToken) {
		t.Errorf("err=%v want ErrMalformedToken", err)
	}
}
