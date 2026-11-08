package chat_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The composer's outbox (plugins/chat/outbox.ts) and a new session's first messages (session-start.ts) are browser-side
// TypeScript, vendored verbatim (apps/p2/web/upstream) and served as Pi's client. Their tests, Pi's outbox.test.ts (4
// cases) and session-start.test.ts (5 cases), run unchanged on the vendored modules under Node.
func TestOutboxAndSessionStartRunPisTests(t *testing.T) {
	oracle.RunNodeTests(t, "test/p2/outbox.test.ts", "test/p2/session-start.test.ts")
}
