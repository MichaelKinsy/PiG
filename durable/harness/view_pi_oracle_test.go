package harness

// pi: packages/durable/src/harness/view.ts (ConversationViews attach/advance/build), agent.ts (configure, addTools)

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type viewItem struct {
	T     string            `json:"t"`
	Conv  int               `json:"conv"`
	Head  any               `json:"head,omitempty"`
	Model []json.RawMessage `json:"model,omitempty"`
	// Change is a JSON object of the fields configure sets; null clears one.
	Change json.RawMessage `json:"change,omitempty"`
	Names  []string        `json:"names"`
}

type viewStep struct {
	Op    string     `json:"op"`
	Ops   []viewItem `json:"ops,omitempty"`
	Label string     `json:"label,omitempty"`
}

func viewPrograms() [][]viewStep {
	random := rand.New(rand.NewSource(20261028))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	names := func(max int) []string {
		list := []string{}
		for range random.Intn(max + 1) {
			list = append(list, pick("a", "b", "c", "d"))
		}
		return list
	}
	nullable := func(value any) any {
		if random.Intn(4) == 0 {
			return nil
		}
		return value
	}
	change := func() json.RawMessage {
		fields := map[string]any{}
		if random.Intn(3) == 0 {
			fields["thinkingLevel"] = nullable(pick("low", "high", "off"))
		}
		if random.Intn(3) == 0 {
			fields["instructions"] = nullable(pick("", "be brief", "multi\nline"))
		}
		if random.Intn(3) == 0 {
			fields["cwd"] = nullable(pick("/", "/work"))
		}
		if random.Intn(3) == 0 {
			switch random.Intn(3) {
			case 0:
				fields["tools"] = names(4)
			case 1:
				fields["tools"] = map[string]any{"remove": names(3)}
			default:
				fields["tools"] = nil
			}
		}
		if random.Intn(3) == 0 {
			switch random.Intn(4) {
			case 0:
				fields["extensions"] = names(4)
			case 1:
				edit := map[string]any{}
				if random.Intn(2) == 0 {
					edit["add"] = names(3)
				}
				if random.Intn(2) == 0 {
					edit["remove"] = names(3)
				}
				fields["extensions"] = edit
			default:
				fields["extensions"] = nil
			}
		}
		encoded, _ := json.Marshal(fields)
		return encoded
	}
	programs := make([][]viewStep, 0, 2500)
	for range 2500 {
		var program []viewStep
		for range 4 + random.Intn(14) {
			switch random.Intn(8) {
			case 0, 1:
				program = append(program, viewStep{Op: "attach", Label: pick("a", "b")})
			case 2:
				program = append(program, viewStep{Op: "detach", Label: pick("a", "b")})
			default:
				step := viewStep{Op: "commit"}
				for range 1 + random.Intn(3) {
					switch random.Intn(5) {
					case 0, 1:
						item := viewItem{T: "append", Conv: random.Intn(5) / 4, Names: []string{}}
						if random.Intn(2) == 0 {
							item.Model = []json.RawMessage{json.RawMessage(`{"role":"user","content":"x","timestamp":1}`)}
						}
						switch random.Intn(6) {
						case 0:
							item.Head = "self"
						case 1:
							item.Head = random.Intn(30)
						}
						step.Ops = append(step.Ops, item)
					case 2:
						step.Ops = append(step.Ops, viewItem{T: "addTools", Names: names(3)})
					default:
						step.Ops = append(step.Ops, viewItem{T: "configure", Change: change(), Names: []string{}})
					}
				}
				program = append(program, step)
			}
		}
		programs = append(programs, program)
	}
	return programs
}

func TestConversationViewsMatchPi(t *testing.T) {
	programs := viewPrograms()
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/view_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
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
	decodeMessages := func(raws []json.RawMessage) []ai.Message {
		var out []ai.Message
		for _, raw := range raws {
			message, err := durable.DecodeMessage(raw)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, message)
		}
		return out
	}
	ctx := context.Background()
	differing, events, headMarkers, errorSteps := 0, 0, 0, 0
	for i, program := range programs {
		store := storage.NewMemoryStorage()
		line := session.CreateSession(store)
		views := NewConversationViews(line, store)
		var conversations [2]durable.ConversationId
		started := false
		var entries []durable.EntryId
		type recorded struct {
			Step  int
			Value ConversationView
			Ops   []durable.Op
		}
		type viewLog struct {
			Initial ConversationView
			Events  []recorded
		}
		logs := map[string]*viewLog{}
		detachers := map[string]func(){}
		current := 0
		pickEntry := func(ref int) durable.EntryId {
			if len(entries) == 0 {
				return 1_000_000_000
			}
			return entries[ref%len(entries)]
		}
		for step, op := range program {
			current = step
			var gotErr error
			switch op.Op {
			case "commit":
				var added []durable.EntryId
				_, gotErr = line.Commit(ctx, func(tx durable.Tx) (any, error) {
					if !started {
						root, err := tx.(*session.Transaction).CreateRootConversation()
						if err != nil {
							return nil, err
						}
						other, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
						if err != nil {
							return nil, err
						}
						conversations = [2]durable.ConversationId{root.Id, other.Id}
						started = true
					}
					for _, item := range op.Ops {
						switch item.T {
						case "append":
							draft := durable.EntryDraft{Kind: "k", Model: decodeMessages(item.Model)}
							switch head := item.Head.(type) {
							case string:
								draft.HeadSelf = true
							case int:
								draft.Head = new(pickEntry(head))
							}
							record, err := tx.AppendEntry(conversations[item.Conv], draft)
							if err != nil {
								return nil, err
							}
							if item.Conv == 0 {
								added = append(added, record.Id)
							}
							if item.Head != nil {
								headMarkers++
							}
						case "addTools":
							if err := AddTools(tx, conversations[0], item.Names); err != nil {
								return nil, err
							}
						default:
							change, err := agentChange(item.Change)
							if err != nil {
								return nil, err
							}
							if err := Configure(tx, conversations[0], change); err != nil {
								return nil, err
							}
						}
					}
					return nil, nil
				})
				if gotErr == nil {
					entries = append(entries, added...)
				} else {
					errorSteps++
				}
			case "attach":
				if !started {
					gotErr = context.Canceled
					break
				}
				entry := &viewLog{}
				_, detach, err := views.Attach(ctx, conversations[0], func(value ConversationView, _ func(), _ durable.Storage) (*ViewObserver, error) {
					entry.Initial = value
					return &ViewObserver{Advance: func(_ context.Context, next ConversationView, ops []durable.Op) {
						entry.Events = append(entry.Events, recorded{current, next, ops})
					}, End: func(error) {}}, nil
				})
				gotErr = err
				if err == nil {
					detachers[op.Label] = detach
					logs[op.Label] = entry
				}
			default:
				if detach := detachers[op.Label]; detach != nil {
					detach()
					delete(detachers, op.Label)
				}
			}
			var want struct {
				OK  *bool   `json:"ok"`
				Err *string `json:"err"`
			}
			_ = json.Unmarshal(expected[i][step], &want)
			if (gotErr == nil) != (want.Err == nil) || (want.Err != nil && op.Op != "attach" && gotErr.Error() != *want.Err) {
				differing++
				if differing <= 5 {
					t.Errorf("program %d step %d (%s): Pig err %v, Pi %s", i, step, op.Op, gotErr, expected[i][step])
				}
			}
		}
		var want struct {
			Views map[string]struct {
				Initial any `json:"initial"`
				Events  []struct {
					Step  int `json:"step"`
					Value any `json:"value"`
					Ops   any `json:"ops"`
				} `json:"events"`
			} `json:"views"`
		}
		if err := json.Unmarshal(expected[i][len(program)], &want); err != nil {
			t.Fatal(err)
		}
		for label, wantLog := range want.Views {
			log := logs[label]
			if log == nil {
				t.Fatalf("program %d: no log for %s", i, label)
			}
			gotEvents := []map[string]any{}
			for _, event := range log.Events {
				gotEvents = append(gotEvents, map[string]any{"step": event.Step, "value": canonical(event.Value), "ops": canonical(event.Ops)})
			}
			wantEvents := []any{}
			for _, event := range wantLog.Events {
				wantEvents = append(wantEvents, map[string]any{"step": float64(event.Step), "value": event.Value, "ops": event.Ops})
			}
			events += len(gotEvents)
			if !reflect.DeepEqual(canonical(log.Initial), wantLog.Initial) || !reflect.DeepEqual(canonical(gotEvents), canonical(wantEvents)) {
				differing++
				if differing <= 5 {
					progJSON, _ := json.Marshal(program)
					gotJSON, _ := json.Marshal(map[string]any{"initial": log.Initial, "events": gotEvents})
					wantJSON, _ := json.Marshal(wantLog)
					t.Errorf("program %d view %s\n  program %.1800s\n  Pi  %.1500s\n  Pig %.1500s", i, label, progJSON, wantJSON, gotJSON)
				}
			}
		}
	}
	t.Logf("%d programs, %d events, %d head-marker appends, %d failed commits, %d differing", len(programs), events, headMarkers, errorSteps, differing)
	if differing > 0 {
		t.Errorf("%d differences from Pi", differing)
	}
}

// agentChange builds an AgentChange from the JSON object of configure fields; null clears a field and an absent one leaves it.
func agentChange(raw json.RawMessage) (AgentChange, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return AgentChange{}, err
	}
	var change AgentChange
	isNull := func(value json.RawMessage) bool { return string(value) == "null" }
	if value, ok := fields["thinkingLevel"]; ok {
		if isNull(value) {
			change.ThinkingLevel = Cleared[ai.ModelThinkingLevel]()
		} else {
			var level ai.ModelThinkingLevel
			_ = json.Unmarshal(value, &level)
			change.ThinkingLevel = SetTo(level)
		}
	}
	if value, ok := fields["instructions"]; ok {
		if isNull(value) {
			change.Instructions = Cleared[string]()
		} else {
			var text string
			_ = json.Unmarshal(value, &text)
			change.Instructions = SetTo(text)
		}
	}
	if value, ok := fields["cwd"]; ok {
		if isNull(value) {
			change.Cwd = Cleared[string]()
		} else {
			var text string
			_ = json.Unmarshal(value, &text)
			change.Cwd = SetTo(text)
		}
	}
	extensions := func(names []string) []*durable.Extension {
		list := []*durable.Extension{}
		for _, name := range names {
			list = append(list, &durable.Extension{Name: name})
		}
		return list
	}
	tools := func(names []string) []*durable.ToolRegistration {
		list := []*durable.ToolRegistration{}
		for _, name := range names {
			list = append(list, &durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: name}})
		}
		return list
	}
	if value, ok := fields["tools"]; ok {
		var list []string
		var edit struct {
			Remove []string `json:"remove"`
		}
		switch {
		case isNull(value):
			change.Tools = Cleared[ToolChange]()
		case json.Unmarshal(value, &list) == nil:
			change.Tools = SetTo(ToolChange{List: tools(list)})
		default:
			if err := json.Unmarshal(value, &edit); err != nil {
				return AgentChange{}, err
			}
			change.Tools = SetTo(ToolChange{Edit: &ToolEdit{Remove: tools(append([]string{}, edit.Remove...))}})
		}
	}
	if value, ok := fields["extensions"]; ok {
		var list []string
		var edit struct {
			Add    []string `json:"add"`
			Remove []string `json:"remove"`
		}
		switch {
		case isNull(value):
			change.Extensions = Cleared[ExtensionChange]()
		case json.Unmarshal(value, &list) == nil:
			change.Extensions = SetTo(ExtensionChange{List: extensions(list)})
		default:
			if err := json.Unmarshal(value, &edit); err != nil {
				return AgentChange{}, err
			}
			next := &ExtensionEdit{}
			if edit.Add != nil {
				next.Add = extensions(edit.Add)
			}
			if edit.Remove != nil {
				next.Remove = extensions(edit.Remove)
			}
			change.Extensions = SetTo(ExtensionChange{Edit: next})
		}
	}
	return change, nil
}
