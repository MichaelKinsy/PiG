//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// A Node tool that awaits real work and then takes the file mutation queue has its next fileMutation.enter held until the host says the edit and write calls waiting for the tool's start window registered
// (NotifyToolWindowSettled). A call that holds no start window (a tool run outside a parallel batch of the agent's dispatcher: a sequential batch, a direct Execute) has nothing waiting, so the host must say
// so at once: without the notification the tool never takes the queue and never answers. The upstream subagent example is such a tool (TestSubagentProjectTrustUpstream).
func TestNodeToolWithoutStartWindowTakesTheFileMutationQueueAfterAwaiting(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	dir := t.TempDir()
	entry := filepath.Join(dir, "await-then-queue.mjs")
	write(t, entry, `import { withFileMutationQueue } from "@earendil-works/pi-coding-agent";
export default function (pi) {
	pi.registerTool({
		name: "await_then_queue",
		label: "await_then_queue",
		description: "Await real work, then take the file mutation queue",
		parameters: { type: "object", properties: {} },
		async execute() {
			await new Promise((resolve) => setTimeout(resolve, 20));
			await withFileMutationQueue(`+"`"+filepath.ToSlash(filepath.Join(dir, "target.txt"))+"`"+`, async () => {});
			return { content: [{ type: "text", text: "took the queue" }], details: {} };
		},
	});
}
`)
	h := newTestHost(t)
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	loaded, err := h.Load(ctx, ExtConfig{Name: "await-then-queue", Source: entry, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := loaded.Tools["await_then_queue"]
	if !ok {
		t.Fatal("tool was not registered")
	}
	result, err := tool.Definition.Execute(ctx, "call-1", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("the tool never took the file mutation queue: %v", err)
	}
	if got := result.Text(); got != "took the queue" {
		t.Fatalf("result = %q", got)
	}
}
