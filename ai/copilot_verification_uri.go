package ai

// Ports packages/ai/src/auth/oauth/github-copilot.ts.

import (
	"errors"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// normalizeCopilotVerificationURI is startDeviceFlow's check of verification_uri (github-copilot.ts:242-257): `new URL(verificationUri)` must parse with an http: or https: protocol, and the URL's href is what the user is sent to (lowercased scheme and host, default port dropped, path "/" added, control bytes percent-encoded, tab and newline removed).
func normalizeCopilotVerificationURI(raw string) (string, error) {
	parsed, err := nodeurl.ParseURL(raw)
	if err != nil || parsed.Protocol != "https:" && parsed.Protocol != "http:" {
		return "", errors.New("Untrusted verification_uri in device code response")
	}
	return parsed.Href(), nil
}
