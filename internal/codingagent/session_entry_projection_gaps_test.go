package codingagent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

func projectRaw(raw string) []agent.AgentMessage {
	return SessionEntryToContextMessages(sessionentry.DecodeSessionEntry(json.RawMessage(raw)))
}

// session-manager.ts sessionEntryToContextMessages: custom messages, branch summaries, compactions and bash executions project to their context messages with the entry timestamp in milliseconds; a compaction carries its system message first; empty summaries and state-only entries project to nothing.
// Pi: packages/coding-agent/src/core/session-manager.ts sessionEntryToContextMessages.
func TestSessionEntryToContextMessagesProjectsEachEntryKind(t *testing.T) {
	const ts = `"timestamp":"2025-01-02T03:04:05.678Z"`
	const ms = int64(1735787045678)

	custom := projectRaw(`{"type":"custom_message","id":"c1","parentId":null,` + ts + `,"customType":"note","content":"hi","display":true,"details":{"k":1}}`)
	if len(custom) != 1 {
		t.Fatalf("custom message projected %d messages", len(custom))
	}
	want := map[string]any{"role": "custom", "customType": "note", "content": "hi", "display": true, "timestamp": ms}
	if details, err := json.Marshal(custom[0].Custom["details"]); err != nil || string(details) != `{"k":1}` {
		t.Errorf("details = %s, %v", details, err)
	}
	delete(custom[0].Custom, "details")
	if !reflect.DeepEqual(custom[0].Custom, want) {
		t.Errorf("custom = %#v, want %#v", custom[0].Custom, want)
	}
	noDetails := projectRaw(`{"type":"custom_message","id":"c2","parentId":null,` + ts + `,"customType":"note","content":"hi","display":false}`)
	if _, has := noDetails[0].Custom["details"]; has || noDetails[0].Custom["display"] != false {
		t.Errorf("custom without details = %#v", noDetails[0].Custom)
	}

	branch := projectRaw(`{"type":"branch_summary","id":"b1","parentId":null,` + ts + `,"fromId":"f1","summary":"explored"}`)
	if len(branch) != 1 || !reflect.DeepEqual(branch[0].Custom, map[string]any{"role": "branchSummary", "summary": "explored", "fromId": "f1", "timestamp": ms}) {
		t.Errorf("branch summary = %#v", branch)
	}
	if got := projectRaw(`{"type":"branch_summary","id":"b2","parentId":null,` + ts + `,"fromId":"f1","summary":""}`); got != nil {
		t.Errorf("an empty branch summary projected %#v", got)
	}

	compaction := projectRaw(`{"type":"compaction","id":"k1","parentId":null,` + ts + `,"summary":"sum","firstKeptEntryId":"e1","tokensBefore":1234}`)
	if len(compaction) != 1 || !reflect.DeepEqual(compaction[0].Custom, map[string]any{"role": "compactionSummary", "summary": "sum", "tokensBefore": 1234, "timestamp": ms}) {
		t.Errorf("compaction = %#v", compaction)
	}
	withSystem := projectRaw(`{"type":"compaction","id":"k2","parentId":null,` + ts + `,"summary":"sum","firstKeptEntryId":"e1","tokensBefore":1,"systemMessage":{"role":"system","content":"rules","timestamp":7}}`)
	if len(withSystem) != 2 || withSystem[1].Custom["role"] != "compactionSummary" || withSystem[0].Role() != "system" {
		t.Errorf("compaction with a system message = %#v", withSystem)
	}

	wire := projectRaw(`{"type":"bash_execution","id":"x1","parentId":null,` + ts + `,"message":{"role":"bashExecution","command":"ls","output":"o","exitCode":0,"cancelled":false,"truncated":false,"timestamp":9}}`)
	if len(wire) != 1 || wire[0].Custom["command"] != "ls" || wire[0].Custom["timestamp"] != float64(9) {
		t.Errorf("wire-form bash execution = %#v", wire)
	}
	flat := projectRaw(`{"type":"bash_execution","id":"x2","parentId":null,` + ts + `,"command":"ls -l","output":"out","exitCode":2,"cancelled":true,"truncated":true,"fullOutputPath":"/tmp/full","excludeFromContext":true}`)
	if len(flat) != 1 {
		t.Fatalf("flat bash execution projected %d messages", len(flat))
	}
	wantFlat := map[string]any{"role": "bashExecution", "command": "ls -l", "output": "out", "exitCode": float64(2), "cancelled": true, "truncated": true, "fullOutputPath": "/tmp/full", "excludeFromContext": true}
	if !reflect.DeepEqual(flat[0].Custom, wantFlat) {
		t.Errorf("flat bash execution = %#v, want %#v", flat[0].Custom, wantFlat)
	}
	plain := projectRaw(`{"type":"bash_execution","id":"x3","parentId":null,` + ts + `,"command":"pwd","output":"","cancelled":false,"truncated":false}`)
	if code, has := plain[0].Custom["exitCode"]; !has || code != nil {
		t.Errorf("a bash execution without an exit code carries exitCode %#v (present %v), want null", code, has)
	}
	if _, has := plain[0].Custom["fullOutputPath"]; has {
		t.Errorf("fullOutputPath present without a value: %#v", plain[0].Custom)
	}
	if _, has := plain[0].Custom["excludeFromContext"]; has {
		t.Errorf("excludeFromContext present when not set: %#v", plain[0].Custom)
	}

	notExcluded := projectRaw(`{"type":"bash_execution","id":"x4","parentId":null,` + ts + `,"command":"a","output":"b","exitCode":0,"cancelled":true,"truncated":false,"excludeFromContext":false}`)
	if _, has := notExcluded[0].Custom["excludeFromContext"]; has || notExcluded[0].Custom["cancelled"] != true || notExcluded[0].Custom["truncated"] != false || notExcluded[0].Custom["exitCode"] != float64(0) {
		t.Errorf("flat bash execution with excludeFromContext=false = %#v", notExcluded[0].Custom)
	}

	user := projectRaw(`{"type":"message","id":"m1","parentId":null,` + ts + `,"message":{"role":"user","content":"hello","timestamp":1}}`)
	if len(user) != 1 || user[0].Role() != "user" {
		t.Errorf("message entry = %#v", user)
	}
	for _, raw := range []string{`{"type":"label","id":"l","parentId":null,` + ts + `,"targetId":"x"}`, `{"type":"thinking_level_change","id":"t","parentId":null,` + ts + `,"thinkingLevel":"high"}`, `{"type":"unknown_kind","id":"u","parentId":null,` + ts + `}`} {
		if got := projectRaw(raw); got != nil {
			t.Errorf("state-only entry projected %#v: %s", got, raw)
		}
	}
	entry := sessionentry.DecodeSessionEntry(json.RawMessage(`{"type":"custom_message","id":"c","parentId":null,` + ts + `,"customType":"t","content":null,"display":true}`))
	if got := SessionEntryToContextMessages(entry); len(got) != 1 || !reflect.DeepEqual(got[0].Custom["content"], []any{}) {
		t.Errorf("null custom content = %#v, want an empty list", got)
	}
}
