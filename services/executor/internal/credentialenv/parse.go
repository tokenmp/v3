package credentialenv

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var envNamePattern = regexp.MustCompile(`^EXECUTOR_CREDENTIAL_[A-Z0-9_]{1,96}$`)

func parseMapping(raw []byte) (map[string]string, error) {
	if len(raw) > MaxMappingBytes {
		return nil, ErrMappingTooLarge
	}
	if !utf8.Valid(raw) || len(raw) == 0 {
		return nil, ErrMappingMalformed
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrMappingMalformed
	}
	out := make(map[string]string)
	for decoder.More() {
		keyToken, err := decoder.Token()
		ref, ok := keyToken.(string)
		if err != nil || !ok || !validRef(ref) {
			return nil, ErrMappingMalformed
		}
		if _, duplicate := out[ref]; duplicate {
			return nil, ErrMappingMalformed
		}
		valueToken, err := decoder.Token()
		envName, ok := valueToken.(string)
		if err != nil || !ok || !validEnvName(envName) {
			return nil, ErrMappingMalformed
		}
		out[ref] = envName
		if len(out) > maxMappings {
			return nil, ErrMappingMalformed
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return nil, ErrMappingMalformed
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrMappingMalformed
	}
	return out, nil
}

func validEnvName(value string) bool {
	return len(value) <= maxEnvNameBytes && envNamePattern.MatchString(value)
}

func validRef(value string) bool {
	if len(value) == 0 || len(value) > maxRefBytes || strings.ContainsAny(value, "\r\n") {
		return false
	}
	u, err := url.ParseRequestURI(value)
	if err != nil || u.Scheme != "vault" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	segments := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(segments) == 0 || segments[0] == "" {
		return false
	}
	for _, segment := range segments {
		if !safeSegment(segment) {
			return false
		}
	}
	return true
}

func safeSegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
