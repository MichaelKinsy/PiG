package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi's ctx.reload() reloads every extension against a fresh runtime, and the reloaded extensions see an active ctx at session_start(reload) (agent-session.ts:3660-3699, loader.ts:689). Pig's CLI loader hands the rebuilt runner the build's host runtime, which the compiled-in extensions registered against; Session.Reload must leave it active, or the built-in extensions' session_start(reload) handlers read a stale ctx and RPC reports extension_error.
// mutation-checked: invalidating the old runtime even when the rebuilt runner keeps it (coding/session_tool_registry.go keepsRuntime) emits extension_error from builtin:mcp before the reload's response.
func TestRPCReloadKeepsTheBuiltinExtensionsRuntimeActive(t *testing.T) {
	home := t.TempDir()
	fixture := filepath.Join(t.TempDir(), "reload.mjs")
	if err := os.WriteFile(fixture, []byte(`export default function (pi) {
  pi.registerCommand("doreload", { description: "reload", handler: async (_args, ctx) => { await ctx.reload(); } });
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, t.TempDir(), []string{
		"HOME=" + home, "USERPROFILE=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR=",
		"PIG_TEST_FAUX=1", "PIG_OFFLINE=1",
	}, "--no-session", "--model", "test-faux/faux-1", "-e", fixture)
	p.send(`{"id":"reload","type":"prompt","message":"/doreload"}`)
	p.await("the reload's response", func(record rpcRecord) bool {
		if record["type"] == "extension_error" {
			t.Fatalf("extension_error during reload: %v", record)
		}
		return record["type"] == "response" && record["id"] == "reload"
	})
	p.closeAndWait("the reload answered")
}
