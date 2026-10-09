package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi's pi.appendEntry appends to the in-process session log at once and then emits entry_appended (agent-session.ts:3406-3411), so a handler that appends and then reads ctx.sessionManager.getEntries() sees its entry. The Node runtime sent the append to the host without waiting and read the replicated log, so the same command counted 0 entries where Pi counts 1.
func TestRPCNodeAppendEntryIsVisibleToTheSameHandlerAndEmitsEntryAppended(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fixture := filepath.Join(dir, "append-visible.mjs")
	if err := os.WriteFile(fixture, []byte(`export default function (pi) {
  pi.registerCommand("append-visible", {
    description: "Append an entry and count it",
    handler: async (args, ctx) => {
      pi.appendEntry("append-visible", { text: args });
      const count = ctx.sessionManager.getEntries().filter((entry) => entry.type === "custom" && entry.customType === "append-visible").length;
      ctx.ui.notify("append-visible count=" + count, "info");
    },
  });
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1"}, "--no-session", "-e", fixture)
	p.send(`{"id":"append","type":"prompt","message":"/append-visible first"}`)
	var notified, appended bool
	p.await("append-visible notification and entry_appended", func(record rpcRecord) bool {
		switch record["type"] {
		case "extension_ui_request":
			if record["method"] == "notify" {
				if message, _ := record["message"].(string); message != "" {
					if message != "append-visible count=1" {
						t.Fatalf("notification = %q, want the appended entry counted in the same handler", message)
					}
					notified = true
				}
			}
		case "entry_appended":
			entry, _ := record["entry"].(map[string]any)
			if entry["customType"] == "append-visible" {
				appended = true
			}
		}
		return notified && appended
	})
}
