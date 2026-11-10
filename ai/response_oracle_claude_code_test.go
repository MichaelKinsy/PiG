package ai_test

import (
	"reflect"
	"testing"
)

// The oracle maps Pi 1.5.0's Claude Code release to PiG's (D235) on Pi's side only: a PiG OAuth request that named
// claude-cli/2.1.322 matches Pi's claude-cli/2.1.280, and one that still named 2.1.280 differs.
func TestOracleMapsTheClaudeCodeReleaseOnPisSideOnly(t *testing.T) {
	request := func(userAgent string) map[string]any {
		return map[string]any{"path": "/v1/messages", "method": "POST", "headers": map[string]any{"user-agent": userAgent, "x-app": "cli"}}
	}
	pi := comparablePiRequest(request("claude-cli/2.1.280"), true)
	if got := comparableRequest(request("claude-cli/2.1.322"), true); !reflect.DeepEqual(got, pi) {
		t.Fatalf("PiG's 2.1.322 against Pi's 2.1.280: %v, want %v", got, pi)
	}
	if got := comparableRequest(request("claude-cli/2.1.280"), true); reflect.DeepEqual(got, pi) {
		t.Fatalf("a PiG request that still names 2.1.280 matches Pi's expectation: %v", got)
	}
	if got := comparableRequest(request("explicit/1.0"), true); reflect.DeepEqual(got, pi) {
		t.Fatalf("a configured user-agent matches Pi's Claude Code default: %v", got)
	}
}
