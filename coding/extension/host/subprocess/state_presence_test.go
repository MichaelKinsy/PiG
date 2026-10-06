package subprocess

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi runner.ts:362-374,872-879 and agent-session.ts:3858-3900 distinguish absent, unknown and measured usage.
func TestContextUsageWirePreservesAbsenceAndNull(t *testing.T) {
	zero := 0
	percent := 0.0
	for _, tc := range []struct {
		name  string
		usage *extension.ContextUsage
		want  string
	}{
		{"absent", nil, "null"},
		{"unknown", &extension.ContextUsage{ContextWindow: 128000}, `{"tokens":null,"contextWindow":128000,"percent":null}`},
		{"zero", &extension.ContextUsage{Tokens: &zero, ContextWindow: 128000, Percent: &percent}, `{"tokens":0,"contextWindow":128000,"percent":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewUIBridge(nil)
			b.SetActions(&HostCallbacks{GetContextUsage: func() *extension.ContextUsage { return tc.usage }})
			data, err := json.Marshal(b.Snapshot(nil, 0, false))
			if err != nil {
				t.Fatal(err)
			}
			var snapshot map[string]json.RawMessage
			if err := json.Unmarshal(data, &snapshot); err != nil {
				t.Fatal(err)
			}
			if got := string(snapshot["contextUsage"]); got != tc.want {
				t.Errorf("snapshot usage = %s, want %s", got, tc.want)
			}
			call, err := b.HandleCall("probe", &CallPayload{Method: "getContextUsage"})
			if err != nil {
				t.Fatal(err)
			}
			if string(call.Result) != tc.want {
				t.Errorf("call usage = %s, want %s", call.Result, tc.want)
			}
		})
	}
	b := NewUIBridge(nil)
	call, err := b.HandleCall("probe", &CallPayload{Method: "getContextUsage"})
	if err != nil || string(call.Result) != "null" {
		t.Fatalf("unbound usage: %+v, %v", call, err)
	}
}

func TestNodeStateClearsAbsentValues(t *testing.T) {
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const {Runtime} = await import(pathToFileURL(%q));
const r = new Runtime("/ext/state.mjs");
assert.equal(r.ctx.mode, "print");
assert.equal(r.ctx.getContextUsage(), undefined, "initial usage");
r.applyState({contextUsage: {tokens: 120, contextWindow: 128000, percent: 1}, model: {id: "old", contextWindow: 128000}});
assert.equal(r.ctx.getContextUsage().tokens, 120);
r.applyState({contextUsage: {tokens: null, contextWindow: 128000, percent: null}});
assert.deepEqual(r.ctx.getContextUsage(), {tokens: null, contextWindow: 128000, percent: null});
r.applyState({contextUsage: null, model: null});
assert.equal(r.ctx.getContextUsage(), undefined, "cleared usage");
assert.equal(r.ctx.model, undefined, "cleared model");
r.applyState({contextUsage: {tokens: 0, contextWindow: 128000, percent: 0}});
assert.equal(r.ctx.getContextUsage().tokens, 0, "measured zero");
r.applyState({editorText: "unrelated"});
assert.equal(r.ctx.getContextUsage().contextWindow, 128000, "partial update retains usage");
r.applyState({allTools:[{name:"read"}], commands:[{name:"x"}], allThemes:[{name:"dark"}], flags:{flag:true}});
r.applyState({allTools:null, commands:null, allThemes:null, flags:null});
assert.deepEqual([r.state.allTools, r.state.commands, r.state.allThemes, r.state.flags], [[], [], [], {}]);
assert.equal(r.footerDataProvider().getGitBranch(), null, "absent git branch");
r.applyState({footerData:{gitBranch:"main"}});
assert.equal(r.footerDataProvider().getGitBranch(), "main");
r.applyState({footerData:{}});
assert.equal(r.footerDataProvider().getGitBranch(), null, "cleared git branch");
`, path)
	if out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("state: %v\n%s", err, out)
	}
}

// Drive the real snapshot/dispatch path, including a same-session name clear and an in-memory replacement.
func TestNodeSnapshotClearsPreviousSessionState(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			h := newTestHost(t)
			t.Cleanup(func() { h.Shutdown("test complete") })
			b := NewUIBridge(nil)
			h.SetUIBridge(b)
			name, file := "named", "/session.jsonl"
			model := map[string]any{"id": "old"}
			tools := []string{"read"}
			prompt := "old prompt"
			b.SetActions(&HostCallbacks{
				GetSessionID: func() string { return "session" }, GetSessionName: func() string { return name }, GetSessionFile: func() string { return file },
				GetModelInfo: func() map[string]any { return model }, GetActiveTools: func() []string { return tools }, GetSystemPrompt: func() string { return prompt },
			})
			entry := filepath.Join(t.TempDir(), "state.mjs")
			write(t, entry, `export default function(pi) { pi.registerCommand("state", {handler: (args, ctx) => {
    const actual = JSON.stringify([ctx.sessionManager.getSessionName() ?? null, ctx.sessionManager.getSessionFile() ?? null, ctx.model?.id ?? null, pi.getActiveTools(), ctx.getSystemPrompt()]);
    if (actual !== args) throw new Error(actual + " != " + args);
   }}); }`)
			loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "state", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v %v", loaded, errs)
			}
			cmd := loaded[0].Commands["state"]
			if err := cmd.Handler(t.Context(), `["named","/session.jsonl","old",["read"],"old prompt"]`); err != nil {
				t.Fatal(err)
			}
			name, file, model, tools, prompt = "", "", nil, nil, ""
			if err := cmd.Handler(t.Context(), `[null,null,null,[],""]`); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A Node extension that calls pi.setActiveTools([]) reads back [] at once, and the host action receives an empty list, so
// a deny-all selection survives the wire. A session that narrowed to no tools replicates as [], not as an absent list.
func TestNodeActiveToolsNarrowedToNoneSurvivesTheWire(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			h := newTestHost(t)
			t.Cleanup(func() { h.Shutdown("test complete") })
			b := NewUIBridge(nil)
			h.SetUIBridge(b)
			set := make(chan []string, 1)
			b.SetActions(&HostCallbacks{
				GetActiveTools: func() []string { return []string{} },
				SetActiveTools: func(names []string) { set <- names },
			})
			entry := filepath.Join(t.TempDir(), "deny.mjs")
			write(t, entry, `export default function(pi) { pi.registerCommand("deny", {handler: () => {
    const before = JSON.stringify(pi.getActiveTools());
    pi.setActiveTools([]);
    const after = JSON.stringify(pi.getActiveTools());
    if (before !== "[]" || after !== "[]") throw new Error(before + " " + after);
   }}); }`)
			loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "deny", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v %v", loaded, errs)
			}
			if err := loaded[0].Commands["deny"].Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			if names := <-set; names == nil || len(names) != 0 {
				t.Fatalf("host SetActiveTools got %#v, want an empty non-nil list", names)
			}
		})
	}
}
