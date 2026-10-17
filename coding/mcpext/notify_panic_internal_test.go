package mcpext

import "testing"

// Upstream wraps only the "MCP failed to load" notify of the loading chain in
// try/catch (mcp/index.ts, the .catch after startConnection); every other
// ctx.ui.notify call lets a throw propagate.
func TestNotifyPanicPropagatesExceptOnTheLoadFailurePath(t *testing.T) {
	ctx := EventContext{Notify: func(string, string) { panic("stale ctx") }}

	func() {
		defer func() {
			if recovered := recover(); recovered != "stale ctx" {
				t.Errorf("recovered = %v, want the Notify panic to propagate", recovered)
			}
		}()
		ctx.notify("hello", "info")
	}()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("notifyLoadFailure let %v escape", recovered)
		}
	}()
	ctx.notifyLoadFailure("MCP failed to load: boom")
}
