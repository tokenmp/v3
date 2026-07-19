package password

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// mustHashArgon2id hashes a valid password for tests, failing the test on error.
func mustHashArgon2id(t *testing.T, pw string) string {
	t.Helper()
	h, err := HashArgon2id(pw)
	if err != nil {
		t.Fatalf("HashArgon2id: %v", err)
	}
	return h
}

func mustHashBcrypt(t *testing.T, pw string) string {
	t.Helper()
	// Generate a legacy bcrypt hash for compatibility testing.
	h, err := bcryptHash(pw)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return h
}

func TestValidate_LengthAndEncoding(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		ok   bool
	}{
		{"too short", "short", false},
		{"eleven runes", "12345678901", false},
		{"twelve runes ok", "123456789012", true},
		{"128 runes ok", strings.Repeat("a", 128), true},
		{"129 runes too long", strings.Repeat("a", 129), false},
		{"empty", "", false},
		{"invalid utf8", "pass\xffword123", false},
		{"nul byte", "password\x00123", false},
		{"newline", "password\n123", false},
		{"tab", "password\t123", false},
		{"c1 DEL", "password\x7f123", false},
		{"unicode ok", "пїѕешзłćцü123", true},
		{"emoji ok", "🔐securepass1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.pw)
			if (err == nil) != c.ok {
				t.Errorf("Validate(%q) err=%v want ok=%v", c.pw, err, c.ok)
			}
		})
	}
}

func TestValidate_NoTrimOrNFKC(t *testing.T) {
	// Leading/trailing spaces are part of the password and must be preserved.
	pw := "  leadtrail12  "
	if err := Validate(pw); err != nil {
		t.Fatalf("Validate with spaces: %v", err)
	}
	h, err := HashArgon2id(pw)
	if err != nil {
		t.Fatalf("HashArgon2id: %v", err)
	}
	if err := Compare(h, "  leadtrail12  "); err != nil {
		t.Errorf("Compare with exact spaces: %v", err)
	}
	if err := Compare(h, "leadtrail12"); err == nil {
		t.Error("Compare trimmed password unexpectedly matched")
	}
}

func TestHashArgon2id_PHCFormat(t *testing.T) {
	h := mustHashArgon2id(t, "supersecret123")
	if !IsArgon2id(h) {
		t.Fatalf("expected argon2id PHC, got %q", h)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Errorf("PHC params mismatch: %q", h)
	}
}

func TestCompare_Argon2idMatchMismatch(t *testing.T) {
	h := mustHashArgon2id(t, "supersecret123")
	if err := Compare(h, "supersecret123"); err != nil {
		t.Errorf("match: %v", err)
	}
	if err := Compare(h, "wrongpassword"); !isMismatch(err) {
		t.Errorf("mismatch err=%v want ErrMismatch", err)
	}
}

func TestCompare_BcryptLegacyMatchAndUpgrade(t *testing.T) {
	pw := "legacypassword123"
	h := mustHashBcrypt(t, pw)
	if !IsBcrypt(h) {
		t.Fatalf("expected bcrypt hash")
	}
	if !UpgradeNeeded(h) {
		t.Error("bcrypt hash should need upgrade")
	}
	if err := Compare(h, pw); err != nil {
		t.Errorf("bcrypt match: %v", err)
	}
	if err := Compare(h, "wrong"); !isMismatch(err) {
		t.Errorf("bcrypt mismatch err=%v", err)
	}
}

func TestCompare_BcryptBVariant(t *testing.T) {
	pw := "legacypassword123"
	h, err := bcryptHashVariant(pw, 'b')
	if err != nil {
		t.Fatalf("bcrypt $2b: %v", err)
	}
	if !IsBcrypt(h) {
		t.Fatalf("expected bcrypt $2b")
	}
	if err := Compare(h, pw); err != nil {
		t.Errorf("bcrypt $2b match: %v", err)
	}
}

func TestCompare_InvalidHash(t *testing.T) {
	if err := Compare("not-a-real-hash", "whatever12345"); err != ErrInvalidHash {
		t.Errorf("err=%v want ErrInvalidHash", err)
	}
}

func TestCompareDummy(t *testing.T) {
	// CompareDummy must not panic and must complete without error.
	CompareDummy()
	if dummyArgonHash == "" {
		t.Error("dummyArgonHash not initialized")
	}
	if !IsArgon2id(dummyArgonHash) {
		t.Errorf("dummy hash not argon2id: %q", dummyArgonHash)
	}
}

func TestCompare_Argon2idDoSParamLimit(t *testing.T) {
	// Craft a valid Argon2id PHC string with memory exceeding the safe limit.
	// This should be rejected before the expensive computation runs.
	baseHash := mustHashArgon2id(t, "testpassword123")
	parts := strings.Split(baseHash, "$")
	if len(parts) != 6 {
		t.Fatalf("unexpected PHC format: %q", baseHash)
	}
	// parts[3] is "m=65536,t=3,p=2"
	// Override memory to exceed the limit.
	parts[3] = fmt.Sprintf("m=%d,t=%d,p=%d", MaxMemoryKiB+1, 3, 2)
	dosHash := strings.Join(parts, "$")
	err := Compare(dosHash, "testpassword123")
	if !errors.Is(err, ErrHashParamsExceeded) {
		t.Errorf("err=%v want ErrHashParamsExceeded for memory exceed", err)
	}
	// Override iterations to exceed the limit.
	parts[3] = fmt.Sprintf("m=%d,t=%d,p=%d", 65536, MaxIterations+1, 2)
	dosHash = strings.Join(parts, "$")
	err = Compare(dosHash, "testpassword123")
	if !errors.Is(err, ErrHashParamsExceeded) {
		t.Errorf("err=%v want ErrHashParamsExceeded for iterations exceed", err)
	}
	// Override parallelism to exceed the limit.
	parts[3] = fmt.Sprintf("m=%d,t=%d,p=%d", 65536, 3, MaxParallelism+1)
	dosHash = strings.Join(parts, "$")
	err = Compare(dosHash, "testpassword123")
	if !errors.Is(err, ErrHashParamsExceeded) {
		t.Errorf("err=%v want ErrHashParamsExceeded for parallelism exceed", err)
	}
}

func isMismatch(err error) bool { return err != nil && err.Error() == ErrMismatch.Error() }
