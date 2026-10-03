package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// A nested call to a tool that keeps a call order (edit and write through the file mutation queue, MCP tools through
// their server's lane) reserves its place before it runs. The tool releases the reservation when it honors it
// (tools.runQueued), and the runner releases it when the call ends without reaching the tool, so a ticket must be
// released once: a second Release of a file mutation ticket closes a closed channel.
//
// upstream: nested-tool-calls.ts:171-248 (execute) runs the call through the tool pipeline once; edit.ts runs it in
// withFileMutationQueue (file-mutation-queue.ts:32-45).
func TestNestedToolCallRunnerReleasesAFileMutationReservationOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	queue := tools.NewFileMutationQueue()
	list := []agent.AgentTool{&tools.EditTool{CWD: dir, Queue: queue}}
	runner, _ := newNestedTestRunner(&list, false)

	for _, edit := range [][2]string{{"hello", "bye"}, {"bye", "done"}} {
		args, _ := json.Marshal(map[string]any{"path": "a.txt", "edits": []any{map[string]string{"oldText": edit[0], "newText": edit[1]}}})
		outcome, err := runner.Execute(t.Context(), "call", "edit", args, NestedToolCallOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if outcome.IsError {
			t.Fatalf("edit %q: %s", edit[0], nestedTextOf(outcome.Result))
		}
	}
	if data, _ := os.ReadFile(path); string(data) != "done" {
		t.Fatalf("file = %q, want %q", data, "done")
	}
	// The queue's chain for the path ends with the last release: a new edit runs at once.
	ticket, err := queue.Reserve(path)
	if err != nil {
		t.Fatal(err)
	}
	ticket.Wait()
	ticket.Release()
}

// The caller boundary: a codemode script edits one file twice at once. Pi registers each edit in the file mutation queue
// in call order, so both apply, in order, and the process survives the second release.
func TestCodemodeScriptEditsOneFileInCallOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit := &tools.EditTool{CWD: dir, Queue: tools.NewFileMutationQueue()}
	h := newCodemodeHarness(t, codemodeHarnessOptions{tools: []agent.AgentTool{edit}, activeTools: []string{"codemode", "edit"}})
	quoted, _ := json.Marshal(path)
	result := codemodeRun(t, h, `
		const results = await Promise.all([
			tools.edit({ path: `+string(quoted)+`, edits: [{ oldText: "one", newText: "two" }] }),
			tools.edit({ path: `+string(quoted)+`, edits: [{ oldText: "two", newText: "three" }] }),
		]);
		text(String(results.length));
	`)
	if result.IsError {
		t.Fatalf("codemode failed: %s", codemodeResultText(t, result))
	}
	if got := codemodeResultText(t, result); strings.TrimSpace(got) != "2" {
		t.Fatalf("output = %q", got)
	}
	if data, _ := os.ReadFile(path); string(data) != "three" {
		t.Fatalf("file = %q, want %q", data, "three")
	}
}
