package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Pi 0.87.1 sdk.ts:231-255,405-412 chooses the effective level before bootstrapping the log.
func TestRPCInitialThinkingEntryMatchesState(t *testing.T) {
	f := newUpstreamRPC(t, false, "--thinking", "off")
	p := f.p
	state := f.command("get_state", nil)
	if state["thinkingLevel"] != "off" {
		t.Fatalf("CLI thinking override: %v", state)
	}
	entries := f.command("get_entries", nil)["entries"].([]any)
	var levels []any
	for _, value := range entries {
		entry := value.(map[string]any)
		if entry["type"] == "thinking_level_change" {
			levels = append(levels, entry["thinkingLevel"])
		}
	}
	if !reflect.DeepEqual(levels, []any{state["thinkingLevel"]}) {
		t.Fatalf("initial levels=%v state=%v", levels, state)
	}
	f.finish()
	if got := p.stderr.String(); got != "" {
		t.Fatalf("RPC stderr=%q", got)
	}
}

// Pi rpc-mode.ts:354-363,502-508,538-541 writes synchronous Session events before mutation responses.
func TestRPCMutationEventsPrecedeResponses(t *testing.T) {
	t.Run("thinking", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		for _, command := range []string{"set_thinking_level", "cycle_thinking_level"} {
			start := len(f.seen)
			f.command(command, rpcRecord{"level": "high"})
			events := f.seen[start : len(f.seen)-1]
			if len(events) != 1 || events[0]["type"] != "thinking_level_changed" {
				t.Fatalf("%s events before response=%v", command, events)
			}
		}
		f.finish()
	})
	t.Run("empty compaction", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.p.send(`{"id":"empty","type":"compact"}`)
		response := f.response("empty")
		if response["success"] != false || len(f.seen) < 2 || f.seen[len(f.seen)-2]["type"] != "compaction_end" {
			t.Fatalf("failed compaction events and response=%v", f.seen)
		}
		f.finish()
	})
	t.Run("compaction", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.prompt("Say hello")
		start := len(f.seen)
		f.command("compact", nil)
		events := f.seen[start : len(f.seen)-1]
		if len(events) == 0 || events[len(events)-1]["type"] != "compaction_end" {
			t.Fatalf("compaction events before response=%v", events)
		}
		f.finish()
	})
}

// Pi agent-session-runtime.ts:195-222 opens missing paths and reports cancelled:false.
func TestRPCSwitchMissingCreatesExplicitSession(t *testing.T) {
	f := newUpstreamRPC(t, false)
	before := f.command("get_state", nil)
	path := filepath.Join(t.TempDir(), "missing.jsonl")
	got := f.command("switch_session", rpcRecord{"sessionPath": path})
	if !reflect.DeepEqual(got, rpcRecord{"cancelled": false}) {
		t.Fatal(got)
	}
	state := f.command("get_state", nil)
	if state["sessionFile"] != path || state["sessionId"] == before["sessionId"] || state["messageCount"] != float64(0) {
		t.Fatal(state)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("premature write: %v", err)
	}
	f.prompt("hello")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	f.finish()
}

func TestRPCInvalidForkDiagnostic(t *testing.T) {
	f := newUpstreamRPC(t, false)
	f.p.send(`{"id":"missing","type":"fork","entryId":"absent"}`)
	got := f.response("missing")
	want := rpcRecord{"id": "missing", "type": "response", "command": "fork", "success": false, "error": "Invalid entry ID for forking"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	f.finish()
}
