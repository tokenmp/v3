package jwt

import "os"

// writeFile is a small helper so the test file does not import "os" directly
// for a single call. Kept separate to keep jwt.go focused.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// newJTI generates a fresh token id (jti claim). It is a 16-byte hex string
// from crypto/rand. On the unreachable failure path it falls back to a
// timestamp-derived value so issuance never fails solely because jti entropy
// was unavailable.
func newJTI() string {
	return newJTIImpl()
}
