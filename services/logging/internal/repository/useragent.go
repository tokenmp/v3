package repository

import (
	"strings"
	"unicode/utf8"
)

const maxUserAgentBytes = 512

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
