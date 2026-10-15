package harness

// pi: packages/durable/src/harness/events.ts (translate, messageChanges, toolUpdate)

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type eventsStep struct {
	Ops     []delta.Op        `json:"ops"`
	Changes []json.RawMessage `json:"changes"`
}

type eventsProgram struct {
	Docs  map[string]any `json:"docs"`
	Held  []int          `json:"held"`
	Steps []eventsStep   `json:"steps"`
}

type eventsGen struct {
	random *rand.Rand
	docs   map[string]any
	nextID int
}

func (g *eventsGen) pick(options ...any) any { return options[g.random.Intn(len(options))] }

func (g *eventsGen) id() int { g.nextID++; return g.nextID }

func (g *eventsGen) usage() map[string]any {
	return map[string]any{"input": g.random.Intn(9), "output": g.random.Intn(9), "cacheRead": 0, "cacheWrite": 0, "totalTokens": g.random.Intn(20), "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}
}

func (g *eventsGen) block() map[string]any {
	switch g.random.Intn(3) {
	case 0:
		return map[string]any{"type": "text", "text": g.pick("", "a", "é𝄞").(string)}
	case 1:
		return map[string]any{"type": "thinking", "thinking": g.pick("", "hm").(string)}
	}
	return map[string]any{"type": "toolCall", "id": g.pick("c1", "c2"), "name": "read", "arguments": map[string]any{"path": "a"}}
}

func (g *eventsGen) message() map[string]any {
	content := []any{}
	for range g.random.Intn(3) {
		content = append(content, g.block())
	}
	return map[string]any{"role": "assistant", "content": content, "api": "x", "provider": "p", "model": "m", "usage": g.usage(), "stopReason": "stop", "timestamp": 1}
}

func (g *eventsGen) slot(callID string) map[string]any {
	slot := map[string]any{"callId": callID, "name": "bash", "status": g.pick("pending", "running", "running", "done")}
	if g.random.Intn(2) == 0 {
		slot["taskId"] = g.id()
	}
	if g.random.Intn(2) == 0 {
		slot["output"] = g.pick("", "out", "é").(string)
	}
	if g.random.Intn(3) == 0 {
		slot["details"] = map[string]any{"k": g.random.Intn(3)}
	}
	if g.random.Intn(4) == 0 {
		slot["diagnostics"] = []any{map[string]any{"severity": "warning", "message": "m"}}
	}
	if slot["status"] == "done" && g.random.Intn(2) == 0 {
		slot["entry"] = g.random.Intn(6) + 1
	}
	return slot
}

func (g *eventsGen) initialDocs() map[string]any {
	live := map[string]any{}
	if g.random.Intn(2) == 0 {
		live["run"] = map[string]any{"taskId": g.random.Intn(4) + 1, "inputs": []any{g.random.Intn(3) + 1}}
	}
	if g.random.Intn(2) == 0 {
		generation := map[string]any{"attempt": g.random.Intn(3)}
		if g.random.Intn(2) == 0 {
			generation["message"] = g.message()
		}
		if g.random.Intn(4) == 0 {
			generation["retry"] = map[string]any{"at": 5, "error": "e"}
		}
		if g.random.Intn(4) == 0 {
			generation["deferred"] = map[string]any{"pollAt": g.random.Intn(3)}
		}
		live["generation"] = generation
	}
	if g.random.Intn(2) == 0 {
		tools := []any{}
		for i := range g.random.Intn(3) + 1 {
			tools = append(tools, g.slot(g.pick("c1", "c2", "c3").(string)+string(rune('a'+i))))
		}
		live["tools"] = tools
	}
	if g.random.Intn(3) == 0 {
		live["compactions"] = []any{map[string]any{"taskId": g.random.Intn(4) + 1, "reason": "threshold", "blocking": g.random.Intn(2) == 0, "attempt": 1}}
	}
	docs := map[string]any{}
	if g.random.Intn(5) != 0 {
		docs["pi.live"] = live
	}
	if g.random.Intn(2) == 0 {
		docs["pi.inbox"] = map[string]any{"items": []any{map[string]any{"id": 1, "mode": "steer", "content": "x"}}}
	}
	if g.random.Intn(2) == 0 {
		docs["pi.agent"] = g.pick(map[string]any{}, map[string]any{"cwd": "/a"}, map[string]any{"thinkingLevel": "low"})
	}
	if g.random.Intn(2) == 0 {
		docs["pi.usage"] = map[string]any{"models": map[string]any{}, "tools": map[string]any{}}
	}
	return docs
}

func (g *eventsGen) get(path ...any) any {
	var current any = g.docs
	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			current = typed[segment.(string)]
		case []any:
			index := segment.(int)
			if index >= len(typed) {
				return nil
			}
			current = typed[index]
		default:
			return nil
		}
	}
	return current
}

func (g *eventsGen) length(path ...any) int {
	list, _ := g.get(path...).([]any)
	return len(list)
}

var (
	liveGeneration = []any{"pi.live", "generation"}
	liveMessage    = []any{"pi.live", "generation", "message"}
)

func at(prefix []any, rest ...any) []any { return append(append([]any{"docs"}, prefix...), rest...) }

// ops returns operations for one random action on the current documents; they may be invalid for the state (the driver then skips the step on both sides).
func (g *eventsGen) action() []delta.Op {
	content := g.length(append(liveMessage[:3:3], "content")...)
	tools := g.length("pi.live", "tools")
	index := func(n int) int {
		if n == 0 {
			return 0
		}
		return g.random.Intn(n)
	}
	// of picks a content index holding a block of the given type, so field edits keep the block well typed (Pi sends the block as raw JSON; Pig's typed blocks drop a foreign field).
	of := func(kind string) int {
		var matches []int
		blocks, _ := g.get(append(liveMessage[:3:3], "content")...).([]any)
		for i, block := range blocks {
			if block.(map[string]any)["type"] == kind {
				matches = append(matches, i)
			}
		}
		if len(matches) == 0 {
			return 99 // not a path: the generator drops the candidate
		}
		return matches[g.random.Intn(len(matches))]
	}
	switch g.random.Intn(28) {
	case 0:
		return []delta.Op{{"s", at(liveMessage), g.message()}}
	case 1:
		return []delta.Op{{"s", at(liveGeneration), map[string]any{"attempt": g.random.Intn(3), "message": g.message()}}}
	case 2:
		return []delta.Op{{"p", at(liveMessage, "content"), g.random.Intn(content + 1), 0, []any{g.block()}}}
	case 3:
		return []delta.Op{{"p", at(liveMessage, "content"), 0, 0, []any{g.block(), g.block()}}}
	case 4:
		return []delta.Op{{"a", at(liveMessage, "content", of("text"), "text"), g.pick("x", "é").(string)}}
	case 5:
		return []delta.Op{{"a", at(liveMessage, "content", of("thinking"), "thinking"), "t"}}
	case 6:
		return []delta.Op{{"a", at(liveMessage, "content", of("toolCall"), "arguments", "path"), "p"}}
	case 7:
		return []delta.Op{{"s", at(liveMessage, "content", of("text"), "textSignature"), "sig"}}
	case 8:
		return []delta.Op{{"s", at(liveMessage, "usage", "output"), g.random.Intn(30)}}
	case 9:
		return []delta.Op{{"s", at(liveMessage, "stopReason"), "length"}}
	case 10:
		return []delta.Op{{"a", at(liveMessage, "content", of("text"), "text"), "a"}, {"s", at(liveMessage, "content", of("text"), "text"), "reset"}, {"a", at(liveMessage, "content", of("text"), "text"), "b"}}
	case 11:
		return []delta.Op{{"s", at([]any{"pi.live", "tools"}), []any{g.slot("c1"), g.slot("c2")}}}
	case 12:
		return []delta.Op{{"s", at([]any{"pi.live", "tools", index(tools), "status"}), g.pick("running", "done")}}
	case 13:
		return []delta.Op{{"a", at([]any{"pi.live", "tools", index(tools), "output"}), "more"}}
	case 14:
		return []delta.Op{{"t", at([]any{"pi.live", "tools", index(tools), "output"}), g.random.Intn(3) + 1}, {"a", at([]any{"pi.live", "tools", index(tools), "output"}), "z"}}
	case 15:
		return []delta.Op{{"s", at([]any{"pi.live", "tools", index(tools), "output"}), g.pick("", "reset")}}
	case 16:
		return []delta.Op{{"s", at([]any{"pi.live", "tools", index(tools), "details"}), map[string]any{"k": g.random.Intn(3)}}}
	case 17:
		return []delta.Op{{"d", at([]any{"pi.live", "tools", index(tools), "details"})}, {"d", at([]any{"pi.live", "tools", index(tools), "diagnostics"})}}
	case 18:
		return []delta.Op{{"d", at([]any{"pi.live", "tools"})}}
	case 19:
		return []delta.Op{{"s", at([]any{"pi.live", "generation", "retry"}), map[string]any{"at": g.random.Intn(9), "error": "e"}}, {"s", at([]any{"pi.live", "generation", "deferred"}), map[string]any{"pollAt": g.random.Intn(3)}}}
	case 20:
		return []delta.Op{{"d", at([]any{"pi.live", "generation", "retry"})}, {"d", at([]any{"pi.live", "generation", "message"})}}
	case 21:
		return []delta.Op{{"s", at([]any{"pi.live", "run"}), map[string]any{"taskId": g.random.Intn(4) + 1, "inputs": []any{g.random.Intn(3) + 1}}}}
	case 24:
		return []delta.Op{{"p", at(liveMessage, "content"), index(content), 1, []any{g.block()}}}
	case 25:
		return []delta.Op{{"p", at(liveMessage, "content"), index(content), 1, []any{}}}
	case 26:
		return []delta.Op{{"m", at(liveMessage, "content"), []any{1, 0}}}
	case 27:
		return []delta.Op{{"t", at(liveMessage, "content", of("text"), "text"), 1}}
	case 22:
		return []delta.Op{g.pick(delta.Op{"d", at([]any{"pi.live", "run"})}, delta.Op{"d", at([]any{"pi.live", "generation"})}, delta.Op{"s", at([]any{"pi.live", "compactions"}), []any{map[string]any{"taskId": g.random.Intn(4) + 1, "reason": "threshold", "blocking": true, "attempt": 1}}}, delta.Op{"s", at([]any{"pi.inbox"}), map[string]any{"items": []any{map[string]any{"id": g.random.Intn(3) + 1, "mode": "steer", "content": "x"}}}}, delta.Op{"d", at([]any{"pi.inbox"})}).(delta.Op)}
	}
	return []delta.Op{g.pick(delta.Op{"s", at([]any{"pi.agent"}), map[string]any{"cwd": "/b"}}, delta.Op{"d", at([]any{"pi.agent"})}, delta.Op{"s", at([]any{"pi.usage"}), map[string]any{"models": map[string]any{}, "tools": map[string]any{}}}, delta.Op{"d", at([]any{"pi.usage"})}, delta.Op{"s", at([]any{"pi.live"}), map[string]any{}}).(delta.Op)}
}

func (g *eventsGen) entry(conversation int) map[string]any {
	entry := map[string]any{"id": g.id(), "conversationId": conversation, "kind": "message"}
	switch g.random.Intn(4) {
	case 0:
		m := g.message()
		entry["model"] = []any{m}
	case 1:
		entry["model"] = []any{map[string]any{"role": "toolResult", "toolCallId": g.pick("c1a", "c2a", "c3a", "c1b", "c2b"), "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": "r"}}, "isError": false, "timestamp": 1}}
	case 2:
		entry["model"] = []any{map[string]any{"role": "user", "content": "hi", "timestamp": 1}}
	default:
		entry["kind"] = "note"
	}
	return entry
}

func (g *eventsGen) task(conversation int) map[string]any {
	status := g.pick("running", "completing", "terminal", "terminal")
	state := map[string]any{"status": status}
	if status != "running" {
		outcome := g.pick(map[string]any{"status": "completed"}, map[string]any{"status": "faulted", "error": map[string]any{"message": "boom"}}, map[string]any{"status": "orphaned", "reason": "gone"})
		state["outcome"] = outcome
	}
	if g.random.Intn(2) == 0 {
		state["checkpoint"] = map[string]any{"arguments": map[string]any{"cmd": "ls"}}
	}
	return map[string]any{"id": g.random.Intn(4) + 1, "conversationId": conversation, "kind": g.pick("pi.generation", "pi.generation", "pi.tool", "pi.compaction"), "version": 1, "input": nil, "background": false, "abortRequested": false, "state": state}
}

func (g *eventsGen) changes() []json.RawMessage {
	out := []json.RawMessage{}
	for range g.random.Intn(4) {
		conversation := 1
		if g.random.Intn(6) == 0 {
			conversation = 2
		}
		var change map[string]any
		switch g.random.Intn(3) {
		case 0:
			change = map[string]any{"type": "entry", "value": g.entry(conversation)}
		case 1:
			change = map[string]any{"type": "task", "value": g.task(conversation)}
		default:
			change = map[string]any{"type": "submission", "value": map[string]any{"id": g.random.Intn(6) + 1, "conversationId": conversation, "type": "prompt", "status": g.pick("pending", "done"), "entry": 1}}
		}
		encoded, _ := json.Marshal(change)
		out = append(out, encoded)
	}
	return out
}

func eventsPrograms(t *testing.T) []eventsProgram {
	random := rand.New(rand.NewSource(20261112))
	var programs []eventsProgram
	for range 2000 {
		g := &eventsGen{random: random, nextID: 10}
		g.docs = g.initialDocs()
		initial, _ := json.Marshal(g.docs)
		var program eventsProgram
		_ = json.Unmarshal(initial, &program.Docs)
		for _, id := range []int{1, 2, 3, 4} {
			if random.Intn(3) == 0 {
				program.Held = append(program.Held, id)
			}
		}
		for range 8 {
			step := eventsStep{Changes: g.changes()}
			for range random.Intn(3) + 1 {
				candidate := g.action()
				value, err := chordjson.Decode(eventsJSON(t, map[string]any{"docs": g.docs}))
				if err != nil {
					t.Fatal(err)
				}
				if applied, err := delta.ApplyImmutable(value, candidate); err == nil {
					var state struct {
						Docs map[string]any `json:"docs"`
					}
					_ = json.Unmarshal(eventsJSON(t, applied), &state)
					g.docs = state.Docs
					step.Ops = append(step.Ops, candidate...)
				}
			}
			if step.Ops == nil {
				step.Ops = []delta.Op{}
			}
			program.Steps = append(program.Steps, step)
		}
		programs = append(programs, program)
	}
	return append(programs, equalValueEdits()...)
}

// equalValueEdits sets each watched document to a deep-equal copy. Pi compares documents by identity and applyImmutable gives a set value a new identity, so each such set publishes its event.
func equalValueEdits() []eventsProgram {
	message := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "a"}}, "api": "x", "provider": "p", "model": "m", "usage": map[string]any{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}, "stopReason": "stop", "timestamp": 1}
	docs := map[string]any{
		"pi.live":  map[string]any{"generation": map[string]any{"attempt": 1, "message": message}, "tools": []any{map[string]any{"callId": "c", "name": "bash", "status": "running", "output": "o", "details": map[string]any{"k": 1}, "diagnostics": []any{map[string]any{"severity": "warning", "message": "m"}}}}},
		"pi.inbox": map[string]any{"items": []any{map[string]any{"id": 1, "mode": "steer", "content": "x"}}},
		"pi.agent": map[string]any{"cwd": "/a"},
		"pi.usage": map[string]any{"models": map[string]any{}, "tools": map[string]any{}},
	}
	var programs []eventsProgram
	for _, op := range []delta.Op{
		{"s", []any{"docs", "pi.agent"}, map[string]any{"cwd": "/a"}},
		{"s", []any{"docs", "pi.usage"}, map[string]any{"models": map[string]any{}, "tools": map[string]any{}}},
		{"s", []any{"docs", "pi.inbox"}, map[string]any{"items": []any{map[string]any{"id": 1, "mode": "steer", "content": "x"}}}},
		{"s", []any{"docs", "pi.live", "generation", "message"}, message},
		{"s", []any{"docs", "pi.live", "generation", "message", "stopReason"}, "stop"},
		{"s", []any{"docs", "pi.live", "tools", 0, "details"}, map[string]any{"k": 1}},
		{"s", []any{"docs", "pi.live", "tools", 0, "diagnostics"}, []any{map[string]any{"severity": "warning", "message": "m"}}},
		{"s", []any{"docs", "pi.live", "tools", 0, "output"}, "o"},
	} {
		programs = append(programs, eventsProgram{Docs: docs, Steps: []eventsStep{{Ops: []delta.Op{op}, Changes: []json.RawMessage{}}}})
	}
	return programs
}

func eventsJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// TestTranslateEventsMatchPi runs Pi's translate over seeded programs of view operations and commit changes and compares every published event batch.
func TestTranslateEventsMatchPi(t *testing.T) {
	programs := eventsPrograms(t)
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/events_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(eventsJSON(t, programs))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]struct {
		Skip     bool              `json:"skip"`
		Events   []json.RawMessage `json:"events"`
		Held     []int             `json:"held"`
		Snapshot json.RawMessage   `json:"snapshot"`
		Err      *string           `json:"err"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(programs) {
		t.Fatalf("Pi answered %d programs, want %d", len(expected), len(programs))
	}
	differing, batches, events := 0, 0, 0
	kinds := map[string]int{}
	for index, program := range programs {
		docs := program.Docs
		held := map[durable.TaskId]bool{}
		for _, id := range program.Held {
			held[durable.TaskId(id)] = true
		}
		view := func(docs map[string]any) ConversationView {
			object, err := chordjson.Decode(eventsJSON(t, docs))
			if err != nil {
				t.Fatal(err)
			}
			return ConversationView{Entries: []durable.EntryRecord{}, Docs: ViewDocs{object.(*delta.JsonObject)}}
		}
		before := view(docs)
		for stepIndex, step := range program.Steps {
			want := expected[index][stepIndex]
			// Operations travel as JSON to Pi; decode them the way a Go caller receives them (numbers as JSON numbers).
			decodedOps, err := chordjson.Decode(eventsJSON(t, step.Ops))
			if err != nil {
				t.Fatal(err)
			}
			var ops []delta.Op
			for _, op := range decodedOps.([]any) {
				ops = append(ops, delta.Op(op.([]any)))
			}
			applied, err := applyDocOps(before.Docs, ops)
			if (err != nil) != want.Skip {
				t.Errorf("program %d step %d: Go apply error %v, Pi skip %v", index, stepIndex, err, want.Skip)
				differing++
				break
			}
			if err != nil {
				continue
			}
			after := ConversationView{Entries: []durable.EntryRecord{}, Docs: applied}
			var publication durable.CommitPublication
			for _, raw := range step.Changes {
				var header struct {
					Type  string          `json:"type"`
					Value json.RawMessage `json:"value"`
				}
				if err := json.Unmarshal(raw, &header); err != nil {
					t.Fatal(err)
				}
				switch header.Type {
				case "entry":
					var value durable.EntryRecord
					if err := json.Unmarshal(header.Value, &value); err != nil {
						t.Fatalf("entry %s: %v", header.Value, err)
					}
					publication.Changes = append(publication.Changes, durable.EntryWrite{Value: value})
				case "task":
					var value durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
					if err := json.Unmarshal(header.Value, &value); err != nil {
						t.Fatalf("task %s: %v", header.Value, err)
					}
					publication.Changes = append(publication.Changes, durable.TaskWrite{Value: value})
				default:
					var value durable.SubmissionRecord
					if err := json.Unmarshal(header.Value, &value); err != nil {
						t.Fatalf("submission %s: %v", header.Value, err)
					}
					publication.Changes = append(publication.Changes, durable.SubmissionWrite{Value: value})
				}
			}
			got := translateEvents(1, before, after, ops, publication, held)
			batches++
			events += len(got)
			var gotJSON []any
			for _, event := range got {
				var decoded any
				_ = json.Unmarshal(eventsJSON(t, event), &decoded)
				gotJSON = append(gotJSON, decoded)
			}
			var wantJSON []any
			for _, raw := range want.Events {
				var decoded any
				_ = json.Unmarshal(raw, &decoded)
				kinds[decoded.(map[string]any)["type"].(string)]++
				wantJSON = append(wantJSON, decoded)
			}
			var gotHeld []int
			for id := range held {
				gotHeld = append(gotHeld, int(id))
			}
			sortInts(gotHeld)
			var gotSnapshot, wantSnapshot any
			_ = json.Unmarshal(eventsJSON(t, snapshotOf(after)), &gotSnapshot)
			_ = json.Unmarshal(want.Snapshot, &wantSnapshot)
			if !reflect.DeepEqual(gotSnapshot, wantSnapshot) {
				differing++
				if differing <= 6 {
					t.Errorf("program %d step %d: snapshot\n got  %s\n want %s", index, stepIndex, eventsJSON(t, gotSnapshot), eventsJSON(t, wantSnapshot))
				}
				break
			}
			if !reflect.DeepEqual(gotJSON, wantJSON) || !reflect.DeepEqual(append([]int{}, gotHeld...), append([]int{}, want.Held...)) {
				differing++
				if differing <= 6 {
					t.Errorf("program %d step %d: ops %s changes %s\n got  %s\n want %s\n held got %v want %v", index, stepIndex, eventsJSON(t, step.Ops), eventsJSON(t, step.Changes), eventsJSON(t, gotJSON), eventsJSON(t, wantJSON), gotHeld, want.Held)
				}
				break
			}
			before = after
		}
	}
	t.Logf("event types: %v", kinds)
	t.Logf("%d programs, %d batches, %d events, %d differing", len(programs), batches, events, differing)
	if differing > 0 {
		t.Fatalf("%d programs differ from Pi", differing)
	}
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
