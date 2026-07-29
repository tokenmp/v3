package app

import (
	"strings"
	"unicode/utf8"
)

const maxUserAgentBytes = 512

// sanitizeUserAgent retains only bounded display-safe metadata from the
// client User-Agent header. It repairs invalid UTF-8, replaces control
// characters with spaces, trims surrounding whitespace, and truncates on a
// UTF-8 boundary. No other client request headers are persisted.
func sanitizeUserAgent(raw string) string {
	if raw == "" {
		return ""
	}
	valid := strings.ToValidUTF8(raw, "�")
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, valid)
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) <= maxUserAgentBytes {
		return cleaned
	}
	end := maxUserAgentBytes
	for end > 0 && !utf8.RuneStart(cleaned[end]) {
		end--
	}
	return strings.TrimSpace(cleaned[:end])
}
