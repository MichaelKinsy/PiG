package harness

// pi: packages/durable/src/harness/prompt.ts (replaySections, planSystemEntries)

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type promptView struct {
	Head     bool              `json:"head"`
	Entries  []map[string]any  `json:"entries"`
	Messages []json.RawMessage `json:"messages"`
}

type promptOp struct {
	Op        string            `json:"op"`
	View      *promptView       `json:"view,omitempty"`
	Messages  []json.RawMessage `json:"messages"`
	Desired   [][2]string       `json:"desired"`
	Tools     []json.RawMessage `json:"tools"`
	Timestamp int64             `json:"timestamp"`
}

type promptGen struct{ random *rand.Rand }

func (g *promptGen) pick(options ...string) string { return options[g.random.Intn(len(options))] }

// "__proto__" is not generated: Pi builds a patch with patch[key] = value, which sets the prototype instead of a member for that key, so a section named __proto__ never reaches a patch.
var promptKeys = []string{"a", "b", "c", "d", "1", "10", "2", "z z", "", "é", "4294967294", "4294967295", "01"}

// tool spells a tool declaration: variants of one name differ in description, in parameters and in the order of the parameter members, which Pi's serialized comparison sees.
func (g *promptGen) tool(name string) json.RawMessage {
	description := g.pick("does x", "does y", "")
	parameters := g.pick(`{"type":"object","properties":{"a":{"type":"string"}}}`, `{"properties":{"a":{"type":"string"}},"type":"object"}`, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"number"}}}`, `{"type":"object","properties":{"b":{"type":"number"},"a":{"type":"string"}}}`)
	constrained := g.pick("", "", `,"constrainedSampling":{"type":"json_schema","strict":"auto"}`, `,"constrainedSampling":false`)
	return json.RawMessage(`{"name":` + quote(name) + `,"description":` + quote(description) + `,"parameters":` + parameters + constrained + `}`)
}

func quote(s string) string { encoded, _ := json.Marshal(s); return string(encoded) }

func (g *promptGen) toolList() []json.RawMessage {
	names := []string{"t1", "t2", "t3", "t4", "t5"}
	g.random.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	out := []json.RawMessage{}
	for _, name := range names[:g.random.Intn(5)] {
		out = append(out, g.tool(name))
	}
	return out
}

func (g *promptGen) systemMessage() json.RawMessage {
	var parts []string
	parts = append(parts, `"role":"system"`, `"content":""`)
	if g.random.Intn(3) != 0 {
		var members []string
		seen := map[string]bool{}
		for range g.random.Intn(4) {
			key := g.pick(promptKeys...)
			if seen[key] {
				continue
			}
			seen[key] = true
			value := `null`
			if g.random.Intn(3) != 0 {
				value = quote(g.pick("one", "two", "", "<x>\nt\n</x>"))
			}
			members = append(members, quote(key)+":"+value)
		}
		parts = append(parts, `"sections":{`+strings.Join(members, ",")+`}`)
	}
	if g.random.Intn(3) == 0 {
		var removed []string
		for range g.random.Intn(3) {
			removed = append(removed, `{"name":`+quote(g.pick("t1", "t2", "t3", "t4", "t5"))+`}`)
		}
		parts = append(parts, `"toolsRemoved":[`+strings.Join(removed, ",")+`]`)
	}
	if g.random.Intn(3) != 0 {
		var added []string
		for _, tool := range g.toolList() {
			added = append(added, string(tool))
		}
		parts = append(parts, `"toolsAdded":[`+strings.Join(added, ",")+`]`)
	}
	parts = append(parts, `"timestamp":`+quote("0")[1:2])
	return json.RawMessage("{" + strings.Join(parts, ",") + "}")
}

func (g *promptGen) messages() []json.RawMessage {
	out := []json.RawMessage{}
	for range g.random.Intn(6) {
		if g.random.Intn(5) == 0 {
			out = append(out, json.RawMessage(`{"role":"user","content":"hi","timestamp":1}`))
			continue
		}
		out = append(out, g.systemMessage())
	}
	return out
}

func promptOps() []promptOp {
	g := &promptGen{random: rand.New(rand.NewSource(20261024))}
	var ops []promptOp
	for range 6000 {
		view := &promptView{Head: g.random.Intn(3) == 0, Messages: g.messages()}
		view.Entries = []map[string]any{}
		for index := range 1 + g.random.Intn(5) {
			kind := g.pick("pi.system", "pi.system", "pi.user", "pi.assistant")
			if index == 0 && view.Head {
				kind = g.pick("pi.head", "pi.system")
			}
			view.Entries = append(view.Entries, map[string]any{"id": index + 1, "kind": kind})
		}
		var desired [][2]string
		seen := map[string]bool{}
		for range g.random.Intn(5) {
			key := g.pick(promptKeys...)
			if !seen[key] {
				seen[key] = true
				desired = append(desired, [2]string{key, g.pick("one", "two", "", "<x>\nt\n</x>")})
			}
		}
		if desired == nil {
			desired = [][2]string{}
		}
		ops = append(ops,
			promptOp{Op: "plan", View: view, Desired: desired, Tools: g.toolList(), Timestamp: int64(g.random.Intn(100))},
			promptOp{Op: "replay", Messages: g.messages()})
	}
	return ops
}

func TestPromptPlanningMatchesPi(t *testing.T) {
	ops := promptOps()
	payload, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/prompt_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Value json.RawMessage `json:"value"`
		Err   *string         `json:"err"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	decode := func(raws []json.RawMessage) []ai.Message {
		out := make([]ai.Message, len(raws))
		for i, raw := range raws {
			message, err := durable.DecodeMessage(raw)
			if err != nil {
				t.Fatalf("%s: %v", raw, err)
			}
			out[i] = message
		}
		return out
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
	differing := map[string]int{}
	counts := map[string]int{}
	nonEmpty := map[string]int{}
	for i, op := range ops {
		var got any
		switch op.Op {
		case "replay":
			shown := ReplaySections(decode(op.Messages))
			pairs := [][2]string{}
			for _, key := range shown.Keys() {
				value, _ := shown.Get(key)
				pairs = append(pairs, [2]string{key, value})
			}
			got = pairs
		case "plan":
			view := durable.ContextView{Messages: decode(op.View.Messages)}
			for _, entry := range op.View.Entries {
				view.Entries = append(view.Entries, durable.EntryRecord{Id: durable.EntryId(entry["id"].(int)), Kind: entry["kind"].(string)})
			}
			if op.View.Head {
				view.Head = &view.Entries[0]
			}
			desired := NewOrderedMap[string]()
			for _, pair := range op.Desired {
				desired.Set(pair[0], pair[1])
			}
			var tools []ai.ToolSchema
			for _, raw := range op.Tools {
				var tool ai.ToolSchema
				if err := json.Unmarshal(raw, &tool); err != nil {
					t.Fatal(err)
				}
				tools = append(tools, tool)
			}
			drafts := PlanSystemEntries(view, desired, tools, op.Timestamp)
			planned := []any{}
			for _, draft := range drafts {
				planned = append(planned, planDraftJSON(draft))
			}
			got = planned
		}
		want := expected[i]
		var wantValue any
		if want.Err != nil {
			wantValue = "ERR " + *want.Err
		} else if err := json.Unmarshal(want.Value, &wantValue); err != nil {
			t.Fatal(err)
		}
		counts[op.Op]++
		if list, ok := wantValue.([]any); ok && len(list) > 0 {
			nonEmpty[op.Op]++
		}
		sameOrder := true
		if want.Err == nil {
			gotJSON, _ := json.Marshal(got)
			sameOrder = reflect.DeepEqual(sectionKeyOrders(t, gotJSON, op.Op), sectionKeyOrders(t, want.Value, op.Op))
		}
		if !reflect.DeepEqual(canonical(got), wantValue) || !sameOrder {
			differing[op.Op]++
			if differing[op.Op] <= 3 {
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(wantValue)
				opJSON, _ := json.Marshal(op)
				t.Errorf("%s op %d\n  op  %s\n  Pi  %s\n  Pig %s", op.Op, i, opJSON, wantJSON, gotJSON)
			}
		}
	}
	t.Logf("ops %v non-empty plans %v differing %v", counts, nonEmpty, differing)
	for kind, count := range differing {
		t.Errorf("%s: %d of %d differ from Pi", kind, count, counts[kind])
	}
	for kind, count := range counts {
		if nonEmpty[kind] < count/3 {
			t.Errorf("%s results are mostly empty: %d of %d", kind, nonEmpty[kind], count)
		}
	}
}

// planDraftJSON is the draft as the entry write holds it: the model messages and the context edits.
func planDraftJSON(draft SystemDraft) map[string]any {
	out := map[string]any{"model": draft.Model}
	if len(draft.Edits) > 0 {
		out["edits"] = draft.Edits
	}
	return out
}

// orderedKeys is a JSON object's member names in the order written; a map would lose it.
type orderedKeys []string

func (keys *orderedKeys) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return err
	}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return err
		}
		*keys = append(*keys, name.(string))
		var skipped json.RawMessage
		if err := decoder.Decode(&skipped); err != nil {
			return err
		}
	}
	return nil
}

// sectionKeyOrders lists, for each planned draft's system message, the order of its sections' member names: the one order a decoded map hides. A replay result is a list of pairs and carries its order itself.
func sectionKeyOrders(t *testing.T, encoded []byte, op string) [][]string {
	t.Helper()
	if op != "plan" {
		return nil
	}
	var drafts []struct {
		Model []struct {
			Sections orderedKeys `json:"sections"`
		} `json:"model"`
	}
	if err := json.Unmarshal(encoded, &drafts); err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, draft := range drafts {
		for _, message := range draft.Model {
			out = append(out, message.Sections)
		}
	}
	return out
}
