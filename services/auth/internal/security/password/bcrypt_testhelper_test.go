package password

import "golang.org/x/crypto/bcrypt"

// bcryptHash generates a legacy bcrypt $2a hash for compatibility tests.
func bcryptHash(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// bcryptHashVariant generates a bcrypt hash with the given version prefix
// ($2a / $2b / $2y). bcrypt.GenerateFromPassword always emits $2a, so we
// rewrite the prefix to exercise the $2b compatibility path.
func bcryptHashVariant(pw string, variant byte) (string, error) {
	h, err := bcryptHash(pw)
	if err != nil {
		return "", err
	}
	if len(h) < 3 || h[1] != '2' {
		return h, nil
	}
	b := []byte(h)
	b[2] = variant
	return string(b), nil
}
