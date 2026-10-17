package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

const appendEntryCostExtension = `export default function (pi) {
  pi.registerCommand("append_many", {
    description: "Append entries and report the map writes they cost",
    handler: async (args, ctx) => {
      const appends = Number(args);
      ctx.sessionManager.getEntries();
      const realSet = Map.prototype.set;
      let sets = 0;
      Map.prototype.set = function (...values) { sets++; return realSet.apply(this, values); };
      try {
        for (let i = 0; i < appends; i++) pi.appendEntry("bulk", { i });
      } finally {
        Map.prototype.set = realSet;
      }
      const bulk = ctx.sessionManager.getEntries().filter((entry) => entry.customType === "bulk");
      const leaf = ctx.sessionManager.getLeafId();
      ctx.ui.notify("appended=" + bulk.length + " sets=" + sets + " leafIsLast=" + (leaf === bulk[bulk.length - 1].id) + " chained=" + bulk.every((entry, index) => index === 0 || entry.parentId === bulk[index - 1].id), "info");
    },
  });
}
`

// Upstream SessionManager.appendCustomEntry is O(1): it pushes the entry, sets byId and moves the leaf (session-manager.ts appendCustomEntry, _appendEntry). The Node runtime's pi.appendEntry rebuilt its id index from the whole log on every append, so k appends in one handler over an n-entry Session cost O(k*(n+k)). Over a real resumed Session, k appends must cost one pass over the replicated log for confirmations and one for the id index, plus a bounded number of map writes per append, and each append must still chain to the previous one as the leaf.
func TestRPCNodeAppendEntryCostIsLinearInTheAppends(t *testing.T) {
	t.Parallel()
	const logEntries, appends = 3000, 1000
	home, cwd := t.TempDir(), t.TempDir()
	manager := codingagent.NewSessionManagerWithDir(cwd, filepath.Join(home, "sessions"))
	session, err := manager.Create("append-cost", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range logEntries {
		if _, err := session.AppendCustomEntry("seed", map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	fixture := filepath.Join(t.TempDir(), "append-cost.mjs")
	if err := os.WriteFile(fixture, []byte(appendEntryCostExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, cwd, []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"},
		"--model", "test-faux/faux-1", "--session", session.Path(), "-e", fixture)
	p.send(fmt.Sprintf(`{"id":"append","type":"prompt","message":"/append_many %d"}`, appends))
	var report string
	p.await("append_many report", func(record rpcRecord) bool {
		if record["type"] == "extension_ui_request" && record["method"] == "notify" {
			report, _ = record["message"].(string)
		}
		return report != ""
	})
	var appended, sets int
	var leafIsLast, chained bool
	if _, err := fmt.Sscanf(report, "appended=%d sets=%d leafIsLast=%t chained=%t", &appended, &sets, &leafIsLast, &chained); err != nil {
		t.Fatalf("report %q: %v", report, err)
	}
	if appended != appends || !leafIsLast || !chained {
		t.Fatalf("report %q: want %d chained appends ending at the leaf", report, appends)
	}
	if limit := 2*logEntries + 8*appends; sets > limit {
		t.Fatalf("%d appends over a %d-entry Session made %d map writes, want at most %d (two passes over the log plus a bounded number per append)", appends, logEntries, sets, limit)
	}
}
