package ai

// Ports packages/ai/src/auth/oauth/github-copilot.ts.

import "errors"

// normalizeCopilotVerificationURI is startDeviceFlow's check of verification_uri (github-copilot.ts:243-246): the shared trustedHttpUrl must accept it, and its normalized href is what the user is sent to.
func normalizeCopilotVerificationURI(raw string) (string, error) {
	href := trustedHTTPURL(raw)
	if href == "" {
		return "", errors.New("Untrusted verification_uri in device code response")
	}
	return href, nil
}
