package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const headlessExtensionStateFixture = `import { appendFileSync } from "node:fs";
export default function (pi) {
  for (const event of ["session_start", "agent_end", "session_shutdown"]) {
    pi.on(event, (_event, ctx) => {
      const entries = ctx.sessionManager.getEntries().map((entry) => entry.type === "message" ? "message:" + entry.message.role : entry.type);
      const usage = ctx.getContextUsage();
      appendFileSync(process.env.STATE_REPORT, JSON.stringify({ event, usage: usage === undefined ? "undefined" : usage, entries }) + "\n");
    });
  }
}
`

type headlessExtensionStateRecord struct {
	Event   string          `json:"event"`
	Usage   json.RawMessage `json:"usage"`
	Entries []string        `json:"entries"`
	parsed  *headlessExtensionContextUsage
}

type headlessExtensionContextUsage struct {
	Tokens        *float64 `json:"tokens"`
	ContextWindow int      `json:"contextWindow"`
	Percent       *float64 `json:"percent"`
}

// Pi 0.87.1 binds ctx.getContextUsage to the Session in every mode
// (agent-session.ts:3112 _bindExtensionCore, getContextUsage at 3858), and
// rpc-mode.ts:728-744,804-807 disposes the runtime on stdin end, which emits
// session_shutdown before the Session is disposed
// (agent-session-runtime.ts:406-413). Probed with test-faux/faux-1 and this
// fixture: -p, --mode json and --mode rpc extensions read {tokens,
// contextWindow: 128000, percent} at session_start, agent_end and
// session_shutdown, and session_shutdown sees the persisted assistant entry.
// The context window is distinct from the SDK fallback (undefined).
func TestHeadlessExtensionsReadContextUsageAndShutdown(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	fixture := filepath.Join(t.TempDir(), "state.mjs")
	if err := os.WriteFile(fixture, []byte(headlessExtensionStateFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd, dir := t.TempDir(), t.TempDir(), t.TempDir()
			report := filepath.Join(t.TempDir(), "report.jsonl")
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "pig"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "STATE_REPORT=" + report}
			args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--model", "test-faux/faux-1", "--session-dir", dir, "-e", fixture}
			if mode == "rpc" {
				process := startRPCProcessAt(t, cwd, env, args...)
				process.sendJSON(map[string]any{"id": "prompt", "type": "prompt", "message": "What is 20+22?"})
				process.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
				process.sendJSON(map[string]any{"id": "settled", "type": "get_state"})
				process.await("settled", func(r rpcRecord) bool { return isSuccessResponse(r, "settled") })
				process.closeAndWait("context usage")
				if text := process.stderr.String(); text != "" {
					t.Fatalf("RPC stderr: %s", text)
				}
			} else {
				if mode == "print" {
					args = append(args, "--print")
				} else {
					args = append(args, "--mode", "json")
				}
				cmd := exec.CommandContext(t.Context(), binary, append(args, "What is 20+22?")...)
				cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
				out, err := cmd.CombinedOutput()
				if err != nil || strings.Contains(string(out), "Extension error") {
					t.Fatalf("%s: %v\n%s", mode, err, out)
				}
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			var events []string
			records := map[string]headlessExtensionStateRecord{}
			for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
				var record headlessExtensionStateRecord
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatal(err)
				}
				var usage headlessExtensionContextUsage
				if err := json.Unmarshal(record.Usage, &usage); err != nil {
					t.Fatalf("%s: getContextUsage() = %s, want a context usage object", record.Event, record.Usage)
				}
				record.parsed = &usage
				events = append(events, record.Event)
				records[record.Event] = record
			}
			if want := []string{"session_start", "agent_end", "session_shutdown"}; !slices.Equal(events, want) {
				t.Fatalf("events = %v, want %v", events, want)
			}
			for _, event := range events {
				usage := records[event].parsed
				if usage.ContextWindow != 128000 || usage.Tokens == nil || usage.Percent == nil {
					t.Fatalf("%s: getContextUsage() = %s, want tokens, percent and contextWindow 128000", event, records[event].Usage)
				}
			}
			if usage := records["agent_end"].parsed; *usage.Tokens <= 0 || *usage.Percent <= 0 {
				t.Fatalf("agent_end: getContextUsage() = %s, want the projected transcript size", records["agent_end"].Usage)
			}
			if entries := records["session_shutdown"].Entries; !slices.Contains(entries, "message:assistant") {
				t.Fatalf("session_shutdown entries = %v, want the persisted assistant message", entries)
			}
		})
	}
}
