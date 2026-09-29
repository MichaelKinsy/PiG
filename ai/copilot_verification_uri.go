package ai

// Ports packages/ai/src/auth/oauth/github-copilot.ts.

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// normalizeCopilotVerificationURI keeps terminal control bytes out of the browser URL and permits only HTTP(S) destinations.
func normalizeCopilotVerificationURI(raw string) (string, error) {
	raw = strings.TrimFunc(raw, func(r rune) bool { return r <= ' ' })
	var normalized strings.Builder
	for _, b := range []byte(raw) {
		switch b {
		case '\t', '\r', '\n':
			continue
		default:
			if b < ' ' {
				fmt.Fprintf(&normalized, "%%%02X", b)
			} else {
				normalized.WriteByte(b)
			}
		}
	}
	parsed, err := url.Parse(normalized.String())
	if err == nil {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
	}
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("Untrusted verification_uri in device code response")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}
