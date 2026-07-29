package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeUserAgent(t *testing.T) {
	t.Parallel()

	if got := sanitizeUserAgent("  Client/1.0\r\nInjected  "); got != "Client/1.0  Injected" {
		t.Fatalf("sanitizeUserAgent controls = %q", got)
	}
	if got := sanitizeUserAgent("bad\xffua"); !utf8.ValidString(got) || !strings.Contains(got, "�") {
		t.Fatalf("sanitizeUserAgent invalid UTF-8 = %q", got)
	}
	got := sanitizeUserAgent(strings.Repeat("界", 300))
	if len(got) > maxUserAgentBytes || !utf8.ValidString(got) {
		t.Fatalf("sanitizeUserAgent length/utf8 = %d/%v", len(got), utf8.ValidString(got))
	}
}
