package harness

// pi: packages/durable/src/harness/tool.ts (boundContent, truncated, fromSlot, renderDiagnostics, appendToolResult)

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	chordoutput "github.com/MichaelKinsy/PiG/chord/output"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type toolDiagDesc struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
}

type toolSlotDesc struct {
	Output       *string         `json:"output,omitempty"`
	DroppedBytes *int            `json:"droppedBytes,omitempty"`
	DroppedLines *int            `json:"droppedLines,omitempty"`
	Details      json.RawMessage `json:"details,omitempty"`
	Diagnostics  []toolDiagDesc  `json:"diagnostics,omitempty"`
}

type toolResultDesc struct {
	Output      []json.RawMessage `json:"output,omitempty"`
	IsError     *bool             `json:"isError,omitempty"`
	Details     json.RawMessage   `json:"details,omitempty"`
	Usage       map[string]any    `json:"usage,omitempty"`
	Diagnostics []toolDiagDesc    `json:"diagnostics,omitempty"`
}

type toolOp struct {
	Op         string            `json:"op"`
	Content    []json.RawMessage `json:"content"`
	Limits     *outputLimitsJSON `json:"limits,omitempty"`
	Lines      int               `json:"lines"`
	Bytes      int               `json:"bytes"`
	Retain     string            `json:"retain,omitempty"`
	Slot       *toolSlotDesc     `json:"slot,omitempty"`
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	Call       map[string]any    `json:"call,omitempty"`
	Result     *toolResultDesc   `json:"result,omitempty"`
	Timestamp  int               `json:"timestamp"`
	DurationMs *int64            `json:"durationMs,omitempty"`
}

func toolOps() []toolOp {
	random := rand.New(rand.NewSource(20261026))
	g := &compactionGen{random: random}
	content := func() []json.RawMessage {
		items := []json.RawMessage{}
		for range random.Intn(5) {
			if random.Intn(4) == 0 {
				items = append(items, json.RawMessage(`{"type":"image","data":"aGk=","mimeType":"image/png"}`))
				continue
			}
			encoded, _ := json.Marshal(map[string]any{"type": "text", "text": outputText(random)})
			items = append(items, encoded)
		}
		return items
	}
	details := func() json.RawMessage {
		switch random.Intn(6) {
		case 0:
			return nil
		case 1:
			return json.RawMessage(`null`)
		default:
			return json.RawMessage(g.pick(`1`, `"s"`, `{"b":1,"a":[1,2]}`, `true`, `[]`, `{"2":0,"1":null}`))
		}
	}
	diagnostics := func() []toolDiagDesc {
		var list []toolDiagDesc
		for range random.Intn(4) {
			diagnostic := toolDiagDesc{Severity: g.pick("info", "warn", "error"), Message: outputText(random)}
			if random.Intn(2) == 0 {
				diagnostic.Code = g.pick("a", "invalid_arguments", "x y")
			}
			list = append(list, diagnostic)
		}
		return list
	}
	count := func() int { return random.Intn(30) }
	ops := make([]toolOp, 0, 4000)
	for range 4000 {
		switch random.Intn(4) {
		case 0:
			ops = append(ops, toolOp{Op: "bound", Content: content(), Limits: &outputLimitsJSON{MaxBytes: 1 + random.Intn(40), MaxLines: 1 + random.Intn(6), Retain: g.pick("head", "tail")}})
		case 1:
			ops = append(ops, toolOp{Op: "truncated", Lines: count(), Bytes: count(), Retain: g.pick("", "head", "tail")})
		case 2:
			op := toolOp{Op: "fromSlot", Code: g.pick("tool_failed", "x"), Message: outputText(random)}
			if random.Intn(5) != 0 {
				slot := &toolSlotDesc{Details: details(), Diagnostics: diagnostics()}
				if random.Intn(2) == 0 {
					text := outputText(random)
					slot.Output = &text
				}
				if random.Intn(2) == 0 {
					slot.DroppedBytes = new(count())
				}
				if random.Intn(2) == 0 {
					slot.DroppedLines = new(count())
				}
				op.Slot = slot
			}
			ops = append(ops, op)
		default:
			result := &toolResultDesc{Details: details(), Diagnostics: diagnostics()}
			if random.Intn(3) != 0 {
				result.Output = content()
			}
			switch random.Intn(3) {
			case 0:
				result.IsError = new(true)
			case 1:
				result.IsError = new(false)
			}
			if random.Intn(3) == 0 {
				result.Usage = g.usage(true)
			}
			op := toolOp{Op: "append", Call: map[string]any{"type": "toolCall", "id": g.pick("c1", "c2"), "name": g.pick("bash", "é"), "arguments": map[string]any{}}, Result: result, Timestamp: random.Intn(1000)}
			if random.Intn(2) == 0 {
				op.DurationMs = new(int64(random.Intn(500)))
			}
			ops = append(ops, op)
		}
	}
	return ops
}

func TestToolResultHelpersMatchPi(t *testing.T) {
	ops := toolOps()
	payload, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/tool_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		OK  json.RawMessage `json:"ok"`
		Err *string         `json:"err"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	decodeContent := func(raws []json.RawMessage) []ai.ToolResultMessageContent {
		encoded, _ := json.Marshal(map[string]any{"role": "toolResult", "toolCallId": "x", "toolName": "x", "content": raws, "isError": false, "timestamp": 0})
		message, err := durable.DecodeMessage(encoded)
		if err != nil {
			t.Fatalf("%s: %v", encoded, err)
		}
		return message.(ai.ToolResultMessage).Content
	}
	diagnostics := func(list []toolDiagDesc) []durable.ToolDiagnostic {
		var out []durable.ToolDiagnostic
		for _, d := range list {
			out = append(out, durable.ToolDiagnostic{Severity: durable.ToolDiagnosticSeverity(d.Severity), Message: d.Message, Code: d.Code})
		}
		return out
	}
	jsonValue := func(raw json.RawMessage) durable.JsonValue {
		var value durable.JsonValue
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	canonical := func(value any) any {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err := json.Unmarshal(encoded, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	resultJSON := func(result durable.ToolExecutionResult) map[string]any {
		out := map[string]any{"output": canonical(result.Output), "diagnostics": canonical(result.Diagnostics)}
		if result.IsError != nil {
			out["isError"] = *result.IsError
		}
		if result.HasDetails {
			out["details"] = canonical(result.Details)
		}
		return out
	}
	ctx := context.Background()
	differing := 0
	kinds := map[string]int{}
	for i, op := range ops {
		var got any
		var gotErr error
		switch op.Op {
		case "bound":
			bounded, droppedBytes, droppedLines := boundContent(decodeContent(op.Content), chordoutput.OutputLimits{MaxBytes: op.Limits.MaxBytes, MaxLines: op.Limits.MaxLines, Retain: op.Limits.Retain})
			got = map[string]any{"content": canonical(bounded), "droppedBytes": droppedBytes, "droppedLines": droppedLines}
			if droppedBytes > 0 {
				kinds["bound-truncated"]++
			}
		case "truncated":
			got = canonical(truncated(op.Lines, op.Bytes, op.Retain))
		case "fromSlot":
			var slot *ToolSlot
			if op.Slot != nil {
				slot = &ToolSlot{Output: op.Slot.Output, DroppedBytes: op.Slot.DroppedBytes, DroppedLines: op.Slot.DroppedLines, Diagnostics: diagnostics(op.Slot.Diagnostics)}
				if op.Slot.Details != nil {
					value := jsonValue(op.Slot.Details)
					slot.Details = &value
				}
			}
			got = resultJSON(fromSlot(slot, op.Code, op.Message))
		default:
			result := durable.ToolExecutionResult{Output: decodeContent(op.Result.Output), IsError: op.Result.IsError, Diagnostics: diagnostics(op.Result.Diagnostics)}
			if op.Result.Details != nil {
				result.Details, result.HasDetails = jsonValue(op.Result.Details), true
			}
			if op.Result.Usage != nil {
				encoded, _ := json.Marshal(op.Result.Usage)
				var usage ai.Usage
				if err := json.Unmarshal(encoded, &usage); err != nil {
					t.Fatal(err)
				}
				result.Usage = &usage
			}
			call := ai.ToolCall{ID: op.Call["id"].(string), Name: op.Call["name"].(string)}
			store := storage.NewMemoryStorage()
			line := session.CreateSession(store)
			var recorded *durable.TypedEntry[durable.ToolResultEntryData]
			_, gotErr = line.Commit(ctx, func(tx durable.Tx) (any, error) {
				record, err := tx.(*session.Transaction).CreateRootConversation()
				if err != nil {
					return nil, err
				}
				recorded, err = AppendToolResult(tx, record.Id, call, result, float64(op.Timestamp), op.DurationMs)
				return nil, err
			})
			if gotErr == nil {
				got = map[string]any{"model": canonical(recorded.Model), "data": canonical(recorded.TypedData)}
			}
			kinds["append"]++
		}
		want := expected[i]
		same := (gotErr == nil) == (want.Err == nil)
		if same && gotErr == nil {
			var wantValue any
			_ = json.Unmarshal(want.OK, &wantValue)
			same = reflect.DeepEqual(canonical(got), wantValue)
		}
		if !same {
			differing++
			if differing <= 5 {
				gotJSON, _ := json.Marshal(got)
				opJSON, _ := json.Marshal(op)
				t.Errorf("op %d\n  op  %.600s\n  Pi  %.700s err %v\n  Pig %.700s err %v", i, opJSON, want.OK, ptrOutputText(want.Err), gotJSON, gotErr)
			}
		}
	}
	t.Logf("%d ops %v, %d differing", len(ops), kinds, differing)
	if differing > 0 {
		t.Errorf("%s: %d ops differ from Pi", fmt.Sprint(len(ops)), differing)
	}
}
