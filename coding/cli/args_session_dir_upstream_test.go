package cli

import "testing"

// TestParseFlagsSessionDir mirrors Pi's parseArgs `--session-dir` branch (packages/coding-agent/src/cli/args.ts:140): the flag consumes the
// next argument only when one follows.
func TestParseFlagsSessionDir(t *testing.T) {
	if got := parseArgs([]string{"--session-dir", "/tmp/sessions"}).SessionDir; got != "/tmp/sessions" {
		t.Errorf("SessionDir = %q, want /tmp/sessions", got)
	}
	if got := parseArgs([]string{"--session-dir"}).SessionDir; got != "" {
		t.Errorf("a trailing --session-dir set SessionDir to %q", got)
	}
}
