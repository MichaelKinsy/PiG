//go:build live

package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Live regression coverage for #10424 (provider-session-cache-e2e.test.ts:46-121). Upstream resolves the Codex OAuth access token from ~/.pi/agent/auth.json (ai/test/oauth.ts); PiG's live tests take the resolved token from PIG_LIVE_CODEX_TOKEN and send the requests to the real Codex backend.
func TestProviderSessionCacheE2ELive(t *testing.T) {
	// upstream: packages/durable/test/provider-session-cache-e2e.test.ts:48
	runProviderSessionCacheCase(t, testenv.RequireLiveEnv(t, "PIG_LIVE_CODEX_TOKEN"), "")
}
