package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// sessionEntrySurrogateFixture holds raw Pi session lines whose non-message fields carry lone UTF-16 units. Pi session-manager.ts parses each line with JSON.parse and writes entries with JSON.stringify, so every unit survives as the same \udXXX escape.
var sessionEntrySurrogateFixture = []string{
	`{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-07-01T00:00:00.001Z","message":{"role":"user","content":"hi","timestamp":1}}`,
	`{"type":"session_info","id":"00000002","parentId":"00000001","timestamp":"2026-07-01T00:00:00.002Z","name":"a\ud800b"}`,
	`{"type":"label","id":"00000003","parentId":"00000002","timestamp":"2026-07-01T00:00:00.003Z","targetId":"00000001","label":"l\udfff"}`,
	`{"type":"custom","customType":"c\ud800","data":{"k\ud800":"v\udfff"},"id":"00000004","parentId":"00000003","timestamp":"2026-07-01T00:00:00.004Z"}`,
	`{"type":"custom_message","customType":"cm\ud800","content":"x\ud800","display":true,"details":{"d":"\udfff"},"id":"00000005","parentId":"00000004","timestamp":"2026-07-01T00:00:00.005Z"}`,
	`{"type":"compaction","id":"00000006","parentId":"00000005","timestamp":"2026-07-01T00:00:00.006Z","summary":"s\ud800","firstKeptEntryId":"00000001","tokensBefore":5,"details":{"d":"\ud800"},"fromHook":true}`,
	`{"type":"branch_summary","id":"00000007","parentId":"00000006","timestamp":"2026-07-01T00:00:00.007Z","fromId":"00000001","summary":"b\udfff","details":{"d":"\udfff"},"fromHook":true}`,
	`{"type":"model_change","id":"00000008","parentId":"00000007","timestamp":"2026-07-01T00:00:00.008Z","provider":"p\ud800","modelId":"m\udfff"}`,
}

func writeSessionEntrySurrogateFixture(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, "entries.jsonl")
	header, err := json.Marshal(codingagent.SessionHeader{Type: "session", Version: codingagent.CurrentSessionVersion, ID: "01900000-0000-7000-8000-000000000002", Timestamp: "2026-07-01T00:00:00.000Z", CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	content := string(header) + "\n" + strings.Join(sessionEntrySurrogateFixture, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runSessionEntryRPC runs one RPC process against the session file and returns each successful response line by request ID.
func runSessionEntryRPC(t *testing.T, binary, home, path string, commands ...string) map[string]string {
	t.Helper()
	responses := runSessionEntryRPCResponses(t, binary, home, path, commands...)
	for id, line := range responses {
		var envelope struct{ Success bool }
		if err := json.Unmarshal([]byte(line), &envelope); err != nil || !envelope.Success {
			t.Fatalf("request %s failed: %s", id, line)
		}
	}
	return responses
}

// runSessionEntryRPCResponses returns every response line, successful or not, by request ID.
func runSessionEntryRPCResponses(t *testing.T, binary, home, path string, commands ...string) map[string]string {
	t.Helper()
	cmd := exec.Command(binary, "--offline", "--mode", "rpc", "--model", "test-faux/echo", "--session", path, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "PIG_TEST_FAUX=1", "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"))
	cmd.Stdin = strings.NewReader(strings.Join(commands, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("RPC: %v\n%s", err, stderr.String())
	}
	responses := map[string]string{}
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		var envelope struct {
			ID   string
			Type string
		}
		if line == "" || json.Unmarshal([]byte(line), &envelope) != nil || envelope.Type != "response" {
			continue
		}
		responses[envelope.ID] = line
	}
	for _, command := range commands {
		var request struct{ ID string }
		if err := json.Unmarshal([]byte(command), &request); err != nil {
			t.Fatal(err)
		}
		if _, ok := responses[request.ID]; !ok {
			t.Fatalf("no %s response: %s\n%s", request.ID, stdout.String(), stderr.String())
		}
	}
	return responses
}

type rawRPCTreeNode struct {
	Entry struct {
		ID string `json:"id"`
	} `json:"entry"`
	Children []rawRPCTreeNode `json:"children"`
	Label    json.RawMessage  `json:"label"`
}

func rawRPCTreeLabel(nodes []rawRPCTreeNode, id string) json.RawMessage {
	for _, node := range nodes {
		if node.Entry.ID == id {
			return node.Label
		}
		if label := rawRPCTreeLabel(node.Children, id); label != nil {
			return label
		}
	}
	return nil
}

func TestRPCSessionEntryFieldsPreserveSurrogateEscapes(t *testing.T) {
	home := t.TempDir()
	path := writeSessionEntrySurrogateFixture(t, home)
	binary := buildPigBinaryForSignalTest(t)
	responses := runSessionEntryRPC(t, binary, home, path,
		`{"id":"entries","type":"get_entries"}`,
		`{"id":"state","type":"get_state"}`,
		`{"id":"tree","type":"get_tree"}`,
		`{"id":"messages","type":"get_messages"}`,
		`{"id":"rename","type":"set_session_name","name":"n\ud800"}`,
		`{"id":"renamed","type":"get_state"}`,
	)
	for _, entry := range sessionEntrySurrogateFixture {
		if !strings.Contains(responses["entries"], entry) {
			t.Errorf("get_entries lost raw entry %s\ngot %s", entry, responses["entries"])
		}
	}
	// RawMessage fields keep the response's own spelling, so each comparison is against JSON.stringify output rather than a decoded Go string.
	var state, renamed struct {
		Data struct {
			SessionName json.RawMessage `json:"sessionName"`
		} `json:"data"`
	}
	var tree struct {
		Data struct {
			Tree []rawRPCTreeNode `json:"tree"`
		} `json:"data"`
	}
	var messages struct {
		Data struct {
			Messages []map[string]json.RawMessage `json:"messages"`
		} `json:"data"`
	}
	for id, target := range map[string]any{"state": &state, "renamed": &renamed, "tree": &tree, "messages": &messages} {
		if err := json.Unmarshal([]byte(responses[id]), target); err != nil {
			t.Fatal(err)
		}
	}
	byRole := map[string]map[string]json.RawMessage{}
	for _, message := range messages.Data.Messages {
		var role string
		if err := json.Unmarshal(message["role"], &role); err != nil {
			t.Fatal(err)
		}
		byRole[role] = message
	}
	for _, tc := range []struct {
		field string
		got   json.RawMessage
		want  string
	}{
		{"get_state session_info name", state.Data.SessionName, `"a\ud800b"`},
		{"get_tree label", rawRPCTreeLabel(tree.Data.Tree, "00000001"), `"l\udfff"`},
		{"get_messages custom_message customType", byRole["custom"]["customType"], `"cm\ud800"`},
		{"get_messages custom_message content", byRole["custom"]["content"], `"x\ud800"`},
		{"get_messages custom_message details", byRole["custom"]["details"], `{"d":"\udfff"}`},
		{"get_messages compaction summary", byRole["compactionSummary"]["summary"], `"s\ud800"`},
		{"get_messages branch summary", byRole["branchSummary"]["summary"], `"b\udfff"`},
		{"set_session_name then get_state", renamed.Data.SessionName, `"n\ud800"`},
	} {
		if string(tc.got) != tc.want {
			t.Errorf("%s=%s, want %s", tc.field, tc.got, tc.want)
		}
	}
	for id, line := range responses {
		if strings.Contains(line, `\ufffd`) || strings.Contains(line, "\ufffd") {
			t.Errorf("%s response replaced a lone surrogate: %s", id, line)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, `"type":"session_info"`) || !strings.Contains(last, `"name":"n\ud800"`) {
		t.Errorf("persisted session_info=%q, want the JSON.stringify escape", last)
	}
	reloaded := runSessionEntryRPC(t, binary, home, path, `{"id":"reloaded","type":"get_state"}`)
	if !strings.Contains(reloaded["reloaded"], `"sessionName":"n\ud800"`) {
		t.Errorf("reloaded state=%s, want sessionName n\\ud800", reloaded["reloaded"])
	}
}

func TestRPCSetSessionNameUsesJavaScriptTrim(t *testing.T) {
	// Pi rpc-mode.ts:661-667 rejects command.name.trim() === "" and otherwise stores the name; String.prototype.trim removes BOM and NBSP but keeps NEL.
	home := t.TempDir()
	path := writeSessionEntrySurrogateFixture(t, home)
	cases := []struct {
		name, want string
		rejected   bool
	}{
		{name: " \t", rejected: true},
		{name: "\u00a0", rejected: true},
		{name: "\ufeff", rejected: true},
		{name: " \ufeff\t", rejected: true},
		{name: "\u0085", want: "\u0085"},
		{name: " \u0085 ", want: "\u0085"},
	}
	var commands []string
	for i, tc := range cases {
		name, err := json.Marshal(tc.name)
		if err != nil {
			t.Fatal(err)
		}
		commands = append(commands, fmt.Sprintf(`{"id":"set-%d","type":"set_session_name","name":%s}`, i, name), fmt.Sprintf(`{"id":"state-%d","type":"get_state"}`, i))
	}
	responses := runSessionEntryRPCResponses(t, buildPigBinaryForSignalTest(t), home, path, commands...)
	for i, tc := range cases {
		var set struct {
			Success bool
			Error   string
		}
		var state struct {
			Data struct {
				SessionName string `json:"sessionName"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(responses[fmt.Sprintf("set-%d", i)]), &set); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(responses[fmt.Sprintf("state-%d", i)]), &state); err != nil {
			t.Fatal(err)
		}
		if tc.rejected {
			if set.Success || set.Error != "Session name cannot be empty" {
				t.Errorf("name %q: set_session_name=%s, want the empty-name error", tc.name, responses[fmt.Sprintf("set-%d", i)])
			}
			continue
		}
		if !set.Success || state.Data.SessionName != tc.want {
			t.Errorf("name %q: set=%s sessionName=%q, want %q", tc.name, responses[fmt.Sprintf("set-%d", i)], state.Data.SessionName, tc.want)
		}
	}
}
