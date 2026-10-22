package extensionconformance

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// childEventsProbe is the extension a child session's resource loader loads in the child_events scenario. It appends one JSON line to __LOG__ for each event it receives, with the context facts Pi's extension context gives a child: hasUI, scopedModels, model, isIdle and the answers of the dialogs of a context that has no UI.
const childEventsProbe = `import { appendFileSync } from "node:fs";
export default function (pi) {
  const LOG = __LOG__;
  const record = entry => appendFileSync(LOG, JSON.stringify(entry) + "\n");
  const scoped = ctx => ctx.scopedModels.map(s => s.model.provider + "/" + s.model.id + (s.thinkingLevel === undefined ? "" : "@" + s.thinkingLevel));
  const names = ["session_start", "input", "before_agent_start", "agent_start", "turn_start", "context", "message_start", "message_update", "message_end", "turn_end", "agent_end", "session_shutdown"];
  for (const type of names) pi.on(type, async (event, ctx) => {
    const entry = { type: type + (type.startsWith("message_") ? ":" + event.message.role : "") };
    if (type === "session_start") entry.reason = event.reason;
    if (type === "session_start" || type === "agent_start" || type === "session_shutdown") {
      entry.ctx = { hasUI: ctx.hasUI, scoped: scoped(ctx), model: ctx.model === undefined ? null : ctx.model.provider + "/" + ctx.model.id, isIdle: ctx.isIdle() };
    }
    if (type === "session_start") {
      entry.ui = { notify: String(await ctx.ui.notify("hi")), select: String(await ctx.ui.select("t", ["a", "b"])), confirm: String(await ctx.ui.confirm("t", "m")), input: String(await ctx.ui.input("t")), editor: String(await ctx.ui.editor("t")) };
    }
    record(entry);
  });
}
`

// childExtensionEventsWant is what Pi's own createAgentSession gives the extension its resource loader loaded (the Node row runs Pi's classes and asserts it): bindExtensions delivers session_start, and the prompt's events follow, with hasUI false, the session's scopedModels in ctx.scopedModels and a UI context whose dialogs answer nothing.
//
//go:embed testdata/child-session-extension-events.json
var childExtensionEventsWant string

// checkChildExtensionEvents runs the fixture's child_events tool once with the child's resources selected by option and once with a DefaultResourceLoader the extension built, and compares what the probe recorded with Pi's.
//
// scratch is a directory the caller made before it built its host (the host's own directory, which the host's cleanup leaves for last). The run's files live under it, not in a TempDir of the subtest: a session outlives the extension's dispose of it and the loader's handle, so the extension host a loader started for the probe stays up, with the loader's cwd as its working directory, until the host's Shutdown ends it (TestShutdownWithLiveChildSessionsLeavesNoDescendants). The subtest's TempDir is removed before that Shutdown, and Windows will not remove a directory a process is in.
func checkChildExtensionEvents(t *testing.T, scratch string, ext extension.Extension) {
	t.Helper()
	var want []map[string]any
	if err := json.Unmarshal([]byte(childExtensionEventsWant), &want); err != nil {
		t.Fatal(err)
	}
	for _, via := range []string{"resources", "loader"} {
		t.Run("extension-events-"+via, func(t *testing.T) {
			dir, err := os.MkdirTemp(scratch, "events-"+via+"-")
			if err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(dir, "events.jsonl")
			probe := filepath.Join(dir, "probe.mjs")
			quoted, _ := json.Marshal(logPath)
			if err := os.WriteFile(probe, []byte(strings.ReplaceAll(childEventsProbe, "__LOG__", string(quoted))), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(logPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]any{"probe": probe, "via": via, "cwd": filepath.Join(dir, "project"), "agentDir": filepath.Join(dir, "agent")})
			if err := os.MkdirAll(filepath.Join(dir, "project"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := ext.Tools["child_events"].Definition.Execute(t.Context(), "call", args, nil); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			got := []map[string]any{}
			scanner := bufio.NewScanner(bytes.NewReader(raw))
			for scanner.Scan() {
				var entry map[string]any
				if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
					t.Fatalf("probe log line %q: %v", scanner.Text(), err)
				}
				got = append(got, entry)
			}
			if !reflect.DeepEqual(got, want) {
				gotJSON, _ := json.MarshalIndent(got, "", " ")
				t.Fatalf("the child's extension received\n%s\nwant Pi's\n%s", gotJSON, childExtensionEventsWant)
			}
		})
	}
}
