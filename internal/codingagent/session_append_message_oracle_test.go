package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type appendMessageOracleResult struct {
	Entries []struct {
		Type    string         `json:"type"`
		Message map[string]any `json:"message"`
	} `json:"entries"`
	Parents      []bool   `json:"parents"`
	IDs          []bool   `json:"ids"`
	ContextRoles []string `json:"contextRoles"`
}

// TestAppendMessageAcceptsEachMemberOfPisMessageUnion drives Pi's SessionManager.appendMessage(message: Message | CustomMessage |
// BashExecutionMessage) (session-manager.ts:1204-1214) and Session.AppendMessage with the same three kinds of message, and compares the
// stored entry type, the stored message, the parent chain, the returned ids and the roles of the rebuilt context.
func TestAppendMessageAcceptsEachMemberOfPisMessageUnion(t *testing.T) {
	user := map[string]any{"role": "user", "content": "hello", "timestamp": 1000}
	bash := map[string]any{"role": "bashExecution", "command": "echo hi", "output": "hi\n", "exitCode": 0, "cancelled": false, "truncated": false, "timestamp": 1001}
	bashExcluded := map[string]any{"role": "bashExecution", "command": "ls", "output": "", "cancelled": true, "truncated": true, "fullOutputPath": "/tmp/out", "timestamp": 1002, "excludeFromContext": true}
	customText := map[string]any{"role": "custom", "customType": "note", "content": "text", "display": true, "details": map[string]any{"k": []any{1, "two"}}, "timestamp": 1003}
	customBlocks := map[string]any{"role": "custom", "customType": "blocks", "content": []any{map[string]any{"type": "text", "text": "x"}}, "display": false, "timestamp": 1004}
	cases := [][]map[string]any{{user}, {bash}, {bashExcluded}, {customText}, {customBlocks}, {user, bash, customText, bashExcluded, customBlocks}}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/session_append_message.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []appendMessageOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	toMessage := func(t *testing.T, m map[string]any) SessionMessage {
		t.Helper()
		switch m["role"] {
		case "user":
			return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hello"), Timestamp: 1000}}
		case "bashExecution":
			var out BashExecutionMessage
			wire, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(wire, &out); err != nil {
				t.Fatal(err)
			}
			return out
		default:
			timestamp, _ := oracleJSONNumber(m["timestamp"])
			display, _ := m["display"].(bool)
			customType, _ := m["customType"].(string)
			return extension.CustomMessage{CustomType: customType, Content: m["content"], Display: display, Details: m["details"], Timestamp: int64(timestamp)}
		}
	}
	for i, messages := range cases {
		session := NewSession("append-message", t.TempDir())
		var ids []string
		for _, m := range messages {
			id, err := session.AppendMessage(toMessage(t, m))
			if err != nil {
				t.Fatalf("case %d: %v", i, err)
			}
			ids = append(ids, id)
		}
		entries := session.GetEntries()
		want := expected[i]
		if len(entries) != len(want.Entries) {
			t.Fatalf("case %d: %d entries, Pi stored %d", i, len(entries), len(want.Entries))
		}
		for j, entry := range entries {
			var got struct {
				Type    string         `json:"type"`
				Message map[string]any `json:"message"`
			}
			if err := json.Unmarshal(entry.Raw(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Type != want.Entries[j].Type || !reflect.DeepEqual(got.Message, want.Entries[j].Message) {
				t.Errorf("case %d entry %d: got %s %v, Pi stored %s %v", i, j, got.Type, got.Message, want.Entries[j].Type, want.Entries[j].Message)
			}
			if entry.Base().ID != ids[j] {
				t.Errorf("case %d entry %d: returned id %q, stored id %q", i, j, ids[j], entry.Base().ID)
			}
			if (j == 0) != (entry.Base().ParentID == nil) || (j > 0 && *entry.Base().ParentID != ids[j-1]) {
				t.Errorf("case %d entry %d: parent %v, want the previous entry", i, j, entry.Base().ParentID)
			}
			if !want.Parents[j] || !want.IDs[j] {
				t.Fatalf("case %d: oracle self-check failed", i)
			}
		}
		var roles []string
		for _, m := range session.BuildSessionProjection().Messages {
			roles = append(roles, m.Role())
		}
		if !reflect.DeepEqual(roles, want.ContextRoles) {
			t.Errorf("case %d: context roles %v, Pi %v", i, roles, want.ContextRoles)
		}
	}
}

// TestAppendMessageMapFormWrapsThePiMembers: a bashExecution or custom message in agent.AgentMessage's map form (what the agent loop
// delivers) lands on the typed members' path, and an unsupported SessionMessage is rejected rather than stored as an empty entry.
func TestAppendMessageMapFormWrapsThePiMembers(t *testing.T) {
	session := NewSession("append-map", t.TempDir())
	bashID, err := session.AppendMessage(agent.AgentMessage{Custom: map[string]any{"role": "bashExecution", "command": "pwd", "output": "/\n", "cancelled": false, "truncated": false, "timestamp": float64(5)}})
	if err != nil {
		t.Fatal(err)
	}
	typedID, err := session.AppendMessage(BashExecutionMessage{Command: "pwd", Output: "/\n", Timestamp: 5})
	if err != nil {
		t.Fatal(err)
	}
	entries := session.GetEntries()
	if len(entries) != 2 || entries[0].Base().ID != bashID || entries[1].Base().ID != typedID {
		t.Fatalf("entries=%+v", entries)
	}
	var first, second map[string]any
	if err := json.Unmarshal(entries[0].Raw(), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(entries[1].Raw(), &second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first["message"], second["message"]) || first["type"] != "message" {
		t.Fatalf("map form stored %v, typed form stored %v", first, second)
	}
	if _, err := session.AppendMessage(nil); err == nil {
		t.Fatal("a nil message was stored")
	}
}

// oracleJSONNumber reads a case's numeric member, written as an int or a float.
func oracleJSONNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case float64:
		return number, true
	}
	return 0, false
}
