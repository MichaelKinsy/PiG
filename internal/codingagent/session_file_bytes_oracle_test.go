package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
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
		for range rng.Intn(12) {
			switch rng.Intn(15) {
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
			case 7:
				ops = append(ops, map[string]any{"op": "info", "name": str()})
			case 8:
				ops = append(ops, map[string]any{"op": "compaction", "summary": str(), "first": rng.Intn(20), "tokensBefore": rng.Intn(100000), "details": json.RawMessage(value()), "fromHook": rng.Intn(2) == 0})
			case 10:
				ops = append(ops, map[string]any{"op": "usage", "kind": []string{"title", "compaction", "tool"}[rng.Intn(3)], "provider": "p", "model": "m" + str(), "usage": json.RawMessage(`{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"totalTokens":10,"cost":{"input":0.001,"output":0.5,"cacheRead":0,"cacheWrite":0,"total":0.501}}`), "note": str()})
			case 11:
				ops = append(ops, map[string]any{"op": "branch", "target": rng.Intn(20)})
			case 12:
				op := map[string]any{"op": "branchSummary", "target": rng.Intn(20), "summary": str(), "details": json.RawMessage(value()), "fromHook": rng.Intn(2) == 0}
				if rng.Intn(4) == 0 {
					op["target"] = nil
				}
				if rng.Intn(2) == 0 {
					op["usage"] = json.RawMessage(`{"input":5,"output":6,"cacheRead":0,"cacheWrite":0,"totalTokens":11,"cost":{"input":0,"output":0.25,"cacheRead":0,"cacheWrite":0,"total":0.25}}`)
				}
				ops = append(ops, op)
			case 13:
				replacement := json.RawMessage("null")
				switch rng.Intn(3) {
				case 0:
					replacement = json.RawMessage(`{"content":` + quote(str()) + `}`)
				case 1:
					replacement = json.RawMessage(`{"content":[{"type":"text","text":` + quote(str()) + `}]}`)
				}
				ops = append(ops, map[string]any{"op": "contextEdit", "target": rng.Intn(20), "replacement": replacement})
			default:
				if rng.Intn(6) == 0 {
					ops = append(ops, map[string]any{"op": "reset"})
					continue
				}
				op := map[string]any{"op": "label", "target": rng.Intn(20)}
				if rng.Intn(3) > 0 {
					op["label"] = str()
				}
				ops = append(ops, op)
			}
		}
		scripts = append(scripts, ops)
	}
	input, err := json.Marshal(scripts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/session_file_bytes.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var oracle []struct {
		Text     *string `json:"text"`
		Errors   []int   `json:"errors"`
		Exported string  `json:"exported"`
	}
	if err := json.Unmarshal(output, &oracle); err != nil {
		t.Fatal(err)
	}
	want := make([]string, len(oracle))
	for i, entry := range oracle {
		if entry.Text != nil {
			want[i] = *entry.Text
		}
	}
	// mask rewrites each line member by member in its order, keeping the bytes of every member except the timestamp and the ids.
	// An id is replaced by the position of its entry, so the parent chain and the target of a label or compaction stay compared.
	mask := func(text string, exported ...bool) string {
		var out []string
		ordinal := map[string]int{}
		for line := range strings.SplitSeq(text, "\n") {
			var head struct {
				ID string `json:"id"`
			}
			if json.Unmarshal([]byte(line), &head) == nil && head.ID != "" {
				ordinal[head.ID] = len(ordinal)
			}
		}
		maskID := func(value string) string {
			var id string
			if json.Unmarshal([]byte(value), &id) == nil {
				if n, ok := ordinal[id]; ok {
					return fmt.Sprintf("E%d", n)
				}
			}
			return value
		}
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
				if key == "id" || key == "parentId" || key == "targetId" || key == "firstKeptEntryId" || key == "fromId" {
					value = maskID(value)
					// An export keeps only the current branch, so a reference may name an entry the export no longer has; its random id is not compared.
					if len(exported) > 0 && key != "id" && key != "parentId" && strings.HasPrefix(value, `"`) && value != `"root"` {
						value = `"OUT"`
					}
				} else if key == "timestamp" {
					if !strings.HasPrefix(value, `"`) {
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
		var ids []string
		var thrown []int
		for index, op := range ops {
			var id string
			err = nil
			switch op["op"] {
			case "message":
				var message agent.AgentMessage
				if err := json.Unmarshal(op["message"].(json.RawMessage), &message); err != nil {
					t.Fatalf("%s: %v", op["message"], err)
				}
				id, err = session.AppendMessage(message)
			case "thinking":
				id, err = session.AppendThinkingLevelChange(op["level"].(string))
			case "model":
				id, err = session.AppendModelChange(op["provider"].(string), op["modelId"].(string))
			case "custom":
				id, err = session.AppendCustomEntry(op["customType"].(string), op["data"])
			case "customMessage":
				var content any
				_ = json.Unmarshal(op["content"].(json.RawMessage), &content)
				id, err = session.AppendCustomMessageEntry(op["customType"].(string), content, op["display"].(bool), op["details"])
			case "info":
				id, err = session.AppendSessionInfo(op["name"].(string))
			case "compaction":
				id, err = session.AppendCompaction(op["summary"].(string), ids[op["first"].(int)%len(ids)], op["tokensBefore"].(int), op["details"], op["fromHook"].(bool), nil)
			case "label":
				var label *string
				if text, ok := op["label"].(string); ok {
					label = &text
				}
				id, err = session.AppendLabelChange(ids[op["target"].(int)%len(ids)], label)
			case "usage":
				var usage ai.Usage
				if err := json.Unmarshal(op["usage"].(json.RawMessage), &usage); err != nil {
					t.Fatal(err)
				}
				var entry UsageEntry
				entry, err = session.AppendUsage(op["kind"].(string), op["provider"].(string), op["model"].(string), usage, op["note"].(string))
				id = entry.ID
			case "branch":
				err = session.Branch(ids[op["target"].(int)%len(ids)])
			case "reset":
				session.ResetLeaf()
			case "branchSummary":
				var parent *string
				if target, ok := op["target"].(int); ok {
					parent = &ids[target%len(ids)]
				}
				var usage *ai.Usage
				if raw, ok := op["usage"].(json.RawMessage); ok {
					usage = &ai.Usage{}
					if err := json.Unmarshal(raw, usage); err != nil {
						t.Fatal(err)
					}
				}
				id, err = session.BranchWithSummary(parent, op["summary"].(string), op["details"], op["fromHook"].(bool), usage)
			case "contextEdit":
				var replacement *ContextEditReplacement
				if raw := op["replacement"].(json.RawMessage); string(raw) != "null" {
					replacement = &ContextEditReplacement{}
					if err := json.Unmarshal(raw, replacement); err != nil {
						t.Fatal(err)
					}
				}
				id, err = session.AppendContextEdit(ids[op["target"].(int)%len(ids)], replacement)
			}
			if err != nil {
				// A refused edit or branch is data: Pi refuses the same operations.
				thrown = append(thrown, index)
				continue
			}
			if id != "" {
				ids = append(ids, id)
			}
		}
		if !slices.Equal(thrown, oracle[i].Errors) && (len(thrown) > 0 || len(oracle[i].Errors) > 0) {
			t.Errorf("script %d: PiG refused operations %v, Pi %v", i, thrown, oracle[i].Errors)
		}
		file := session.GetSessionFile()
		raw, readErr := os.ReadFile(*file)
		if readErr != nil {
			t.Fatalf("script %d: %v", i, readErr)
		}
		// serializeSessionBranch (session-export.ts): a fresh header, the current branch with its parentId chained, one JSON line per entry.
		exported, exportErr := SerializeSessionBranch(session.ExportHeader(), BugReportBranch(session), time.Now(), nil)
		if exportErr != nil {
			t.Fatalf("script %d: export: %v", i, exportErr)
		}
		if mask(exported, true) != mask(oracle[i].Exported, true) {
			if failures++; failures <= 4 {
				got, expected := strings.Split(mask(exported, true), "\n"), strings.Split(mask(oracle[i].Exported, true), "\n")
				for j := range min(len(got), len(expected)) {
					if got[j] != expected[j] {
						t.Errorf("script %d exported line %d:\n PiG %.700s\n Pi  %.700s", i, j, got[j], expected[j])
						break
					}
				}
				if len(got) != len(expected) {
					t.Errorf("script %d exported: PiG %d lines, Pi %d", i, len(got), len(expected))
				}
			}
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
