package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/session-manager.ts

// The bytes of a session file (session-manager.ts _persist: `${JSON.stringify(entry)}\n` per entry, the header first, written at the first user
// or assistant message): member order, escaping of U+2028/2029, <, & and DEL, number forms, the optional members. The pinned SessionManager and
// PiG's Session write the same scripted appends; entry ids and timestamps, which differ by design, are masked and everything else must be equal.
func TestSessionFileBytesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	chunks := []string{"a", "é", "😀", "\u2028", "\u2029", "<&>", "\\", "\"", "\n", "\u007f", "\u0001", "\t", "x y"}
	str := func() string {
		var b strings.Builder
		for range rng.Intn(6) {
			b.WriteString(chunks[rng.Intn(len(chunks))])
		}
		return b.String()
	}
	value := func() string {
		switch rng.Intn(7) {
		case 0:
			return []string{"0", "1.5", "100", "0.1", "-3"}[rng.Intn(5)]
		case 1:
			return fmt.Sprintf(`{"z":%s,"a":%s,"m":[%s,{"k":null}]}`, quote(str()), quote(str()), quote(str()))
		case 2:
			return "null"
		case 3:
			return "true"
		default:
			return quote(str())
		}
	}
	message := func() string {
		switch rng.Intn(5) {
		case 0:
			return fmt.Sprintf(`{"role":"user","content":%s,"timestamp":%d}`, quote(str()), rng.Intn(5000))
		case 1:
			return fmt.Sprintf(`{"role":"user","content":[{"type":"text","text":%s}],"timestamp":%d}`, quote(str()), rng.Intn(5000))
		case 2:
			return fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%s},{"type":"thinking","thinking":%s},{"type":"toolCall","id":"c1","name":"read","arguments":{"path":%s,"z":%s,"a":1}}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"totalTokens":10,"cost":{"input":0.001,"output":0.5,"cacheRead":0,"cacheWrite":0,"total":0.501}},"stopReason":"toolUse","timestamp":%d}`, quote(str()), quote(str()), quote(str()), value(), rng.Intn(5000))
		case 3:
			return fmt.Sprintf(`{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":%s}],"isError":false,"timestamp":%d}`, quote(str()), rng.Intn(5000))
		default:
			return fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%s}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":%d}`, quote(str()), rng.Intn(5000))
		}
	}
	var scripts [][]map[string]any
	for range 300 {
		ops := []map[string]any{{"op": "message", "message": json.RawMessage(`{"role":"user","content":"first","timestamp":1}`)}}
		for range rng.Intn(10) {
			switch rng.Intn(8) {
			case 0, 1, 2:
				ops = append(ops, map[string]any{"op": "message", "message": json.RawMessage(message())})
			case 3:
				ops = append(ops, map[string]any{"op": "thinking", "level": []string{"off", "low", "high"}[rng.Intn(3)]})
			case 4:
				ops = append(ops, map[string]any{"op": "model", "provider": "p" + str(), "modelId": "m" + str()})
			case 5:
				ops = append(ops, map[string]any{"op": "custom", "customType": "t" + str(), "data": json.RawMessage(value())})
			case 6:
				ops = append(ops, map[string]any{"op": "customMessage", "customType": "t", "content": json.RawMessage(quote(str())), "display": rng.Intn(2) == 0, "details": json.RawMessage(value())})
			default:
				ops = append(ops, map[string]any{"op": "info", "name": str()})
			}
		}
		scripts = append(scripts, ops)
	}
	input, err := json.Marshal(scripts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/session_file_bytes.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	// mask rewrites each line member by member in its order, keeping the bytes of every member except the entry id, parent id and timestamp.
	mask := func(text string) string {
		var out []string
		for line := range strings.SplitSeq(text, "\n") {
			if line == "" {
				continue
			}
			decoder := json.NewDecoder(strings.NewReader(line))
			if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
				out = append(out, "?"+line)
				continue
			}
			var members []string
			for decoder.More() {
				keyToken, _ := decoder.Token()
				key := keyToken.(string)
				var raw json.RawMessage
				if err := decoder.Decode(&raw); err != nil {
					t.Fatal(err)
				}
				value := string(raw)
				if key == "id" || key == "parentId" || key == "timestamp" {
					if key == "timestamp" && !strings.HasPrefix(value, `"`) {
						members = append(members, key+":"+value) // a message timestamp is a number, written as is
						continue
					}
					value = "MASK"
				}
				members = append(members, `"`+key+`":`+value)
			}
			out = append(out, "{"+strings.Join(members, ",")+"}")
		}
		return strings.Join(out, "\n")
	}
	failures := 0
	for i, ops := range scripts {
		manager := NewSessionManagerWithDir("/work/project", t.TempDir())
		session, err := manager.Create("fixed-id", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range ops {
			switch op["op"] {
			case "message":
				var message agent.AgentMessage
				if err := json.Unmarshal(op["message"].(json.RawMessage), &message); err != nil {
					t.Fatalf("%s: %v", op["message"], err)
				}
				_, err = session.AppendMessage(message)
			case "thinking":
				_, err = session.AppendThinkingLevelChange(op["level"].(string))
			case "model":
				_, err = session.AppendModelChange(op["provider"].(string), op["modelId"].(string))
			case "custom":
				_, err = session.AppendCustomEntry(op["customType"].(string), op["data"])
			case "customMessage":
				var content any
				_ = json.Unmarshal(op["content"].(json.RawMessage), &content)
				_, err = session.AppendCustomMessageEntry(op["customType"].(string), content, op["display"].(bool), op["details"])
			case "info":
				_, err = session.AppendSessionInfo(op["name"].(string))
			}
			if err != nil {
				t.Fatalf("script %d %v: %v", i, op["op"], err)
			}
		}
		file := session.GetSessionFile()
		raw, readErr := os.ReadFile(*file)
		if readErr != nil {
			t.Fatalf("script %d: %v", i, readErr)
		}
		if mask(string(raw)) != mask(want[i]) {
			if failures++; failures <= 4 {
				got, expected := strings.Split(mask(string(raw)), "\n"), strings.Split(mask(want[i]), "\n")
				for j := range min(len(got), len(expected)) {
					if got[j] != expected[j] {
						t.Errorf("script %d line %d:\n PiG %.700s\n Pi  %.700s", i, j, got[j], expected[j])
						break
					}
				}
				if len(got) != len(expected) {
					t.Errorf("script %d: PiG %d lines, Pi %d", i, len(got), len(expected))
				}
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d session files differ from Pi", failures, len(scripts))
	}
}
