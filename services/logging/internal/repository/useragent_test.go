package repository

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeUserAgent(t *testing.T) {
	t.Parallel()

	if got := sanitizeUserAgent("  SDK/1.0\r\nInjected  "); got != "SDK/1.0  Injected" {
		t.Fatalf("controls = %q", got)
	}
	got := sanitizeUserAgent(strings.Repeat("界", 300))
	if len(got) > maxUserAgentBytes || !utf8.ValidString(got) {
		t.Fatalf("length/utf8 = %d/%v", len(got), utf8.ValidString(got))
	}
}
