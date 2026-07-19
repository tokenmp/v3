package jwt

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

// newJTIImpl generates a 16-byte hex jti. If crypto/rand fails (extremely
// unlikely), fall back to a timestamp-derived value so token issuance does not
// fail solely on entropy exhaustion.
func newJTIImpl() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 16)
}
