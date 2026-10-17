package harness

// pi: packages/durable/src/harness/context.ts (readContext, readContextFrom, deriveRange, extendRange, selectActive, contribute, leadWithSystem, settle)

import (
	"bytes"
	"cmp"
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

type contextEditStep struct {
	Target int    `json:"target"`
	Action string `json:"action"`
	// Messages carries no omitempty: a replace edit without messages makes Pi throw (a malformed edit), and an empty list must stay present.
	Messages []json.RawMessage `json:"messages,omitzero"`
}

type contextStep struct {
	Op    string            `json:"op"`
	Kind  string            `json:"kind,omitempty"`
	Model []json.RawMessage `json:"model,omitempty"`
	Edits []contextEditStep `json:"edits,omitempty"`
	Head  any               `json:"head,omitempty"`
	At    *int              `json:"at,omitempty"`
	Mode  string            `json:"mode,omitempty"`
}

func contextPrograms() [][]contextStep {
	random := rand.New(rand.NewSource(20261025))
	g := &compactionGen{random: random}
	p := &promptGen{random: random}
	ref := func() int { return random.Intn(40) }
	message := func() json.RawMessage {
		if random.Intn(8) == 0 {
			return p.systemMessage()
		}
		encoded, _ := json.Marshal(g.message())
		return encoded
	}
	programs := make([][]contextStep, 0, 3000)
	for range 3000 {
		var program []contextStep
		for range 2 + random.Intn(14) {
			step := contextStep{}
			switch random.Intn(7) {
			case 0, 1, 2, 3:
				step.Op = "append"
				// pi.system entries can be the baseline that leads the context (Pi 5713c950).
				if random.Intn(3) == 0 {
					step.Kind = "pi.system"
				}
				if random.Intn(5) != 0 {
					step.Model = []json.RawMessage{}
					for range 1 + random.Intn(3) {
						step.Model = append(step.Model, message())
					}
				}
				if random.Intn(5) == 0 {
					for range 1 + random.Intn(2) {
						edit := contextEditStep{Target: ref(), Action: g.pick("omit", "replace")}
						if edit.Action == "replace" {
							edit.Messages = []json.RawMessage{}
							for range random.Intn(3) {
								edit.Messages = append(edit.Messages, message())
							}
						}
						step.Edits = append(step.Edits, edit)
					}
				}
				switch random.Intn(9) {
				case 0:
					step.Head = "self"
				case 1:
					step.Head = ref()
				}
			default:
				step.Op = "read"
				step.Mode = g.pick("full", "incr", "incr", "incr")
				if random.Intn(4) == 0 {
					step.At = new(ref())
				}
			}
			program = append(program, step)
		}
		program = append(program, contextStep{Op: "read", Mode: "incr"}, contextStep{Op: "read", Mode: "full"})
		programs = append(programs, program)
	}
	return programs
}

// dropEmptyToolLists removes the empty toolsAdded and toolsRemoved arrays Pi keeps on a system message: ai.SystemMessage cannot tell a present empty list from an absent one, so Pig writes neither.
func dropEmptyToolLists(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, member := range v {
			if list, ok := member.([]any); ok && len(list) == 0 && (key == "toolsAdded" || key == "toolsRemoved") {
				delete(v, key)
				continue
			}
			v[key] = dropEmptyToolLists(member)
		}
	case []any:
		for i := range v {
			v[i] = dropEmptyToolLists(v[i])
		}
	}
	return value
}

func TestContextDerivationMatchesPi(t *testing.T) {
	programs := contextPrograms()
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/context_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]struct {
		ID   *int64          `json:"id"`
		Err  *string         `json:"err"`
		View json.RawMessage `json:"view"`
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
	ctx := context.Background()
	differing, reads, withEdits, withHead, appendErrors := 0, 0, 0, 0, 0
	for i, program := range programs {
		store := storage.NewMemoryStorage()
		line := session.CreateSession(store)
		adapter := lineAdapter{line}
		var conversation durable.ConversationId
		started := false
		var ids []durable.EntryId
		var previous *contextRange
		pick := func(ref int) durable.EntryId {
			if len(ids) == 0 {
				return 1_000_000_000
			}
			return ids[ref%len(ids)]
		}
		for j, step := range program {
			want := expected[i][j]
			var got any
			var gotErr error
			switch {
			case step.Op == "append":
				var id durable.EntryId
				_, gotErr = line.Commit(ctx, func(tx durable.Tx) (any, error) {
					if !started {
						record, err := tx.(*session.Transaction).CreateRootConversation()
						if err != nil {
							return nil, err
						}
						conversation = record.Id
						started = true
					}
					draft := durable.EntryDraft{Kind: cmp.Or(step.Kind, "k"), Model: decode(step.Model)}
					for _, edit := range step.Edits {
						draft.Edits = append(draft.Edits, durable.ContextEdit{Target: pick(edit.Target), Action: durable.ContextEditAction(edit.Action), Messages: decode(edit.Messages)})
					}
					switch head := step.Head.(type) {
					case string:
						draft.HeadSelf = true
					case int:
						draft.Head = new(pick(head))
					}
					record, err := tx.AppendEntry(conversation, draft)
					id = record.Id
					return record, err
				})
				if gotErr == nil {
					ids = append(ids, id)
					got = int64(id)
				} else {
					appendErrors++
				}
				if step.Edits != nil {
					withEdits++
				}
				if step.Head != nil {
					withHead++
				}
			case !started:
				got = nil
			default:
				reads++
				var at *durable.EntryId
				if step.At != nil {
					at = new(pick(*step.At))
				}
				var view durable.ContextView
				if step.Mode == "full" {
					view, gotErr = ReadContext(ctx, adapter, store, conversation, at)
				} else {
					var next *contextRange
					view, next, gotErr = readContextFrom(ctx, adapter, store, conversation, at, previous)
					if gotErr == nil {
						previous = next
					}
				}
				if gotErr == nil {
					entries := []int64{}
					for _, entry := range view.Entries {
						entries = append(entries, int64(entry.Id))
					}
					var head any
					if view.Head != nil {
						head = int64(view.Head.Id)
					}
					contributions := make([][]ai.Message, len(view.Contributions))
					for k, c := range view.Contributions {
						contributions[k] = append([]ai.Message{}, c...)
					}
					got = map[string]any{"head": head, "entries": entries, "contributions": contributions, "messages": append([]ai.Message{}, view.Messages...)}
				}
			}
			same := (gotErr == nil) == (want.Err == nil)
			if same && gotErr != nil {
				same = gotErr.Error() == *want.Err
			}
			if same && gotErr == nil {
				var wantValue any
				switch {
				case want.ID != nil:
					wantValue = float64(*want.ID)
					got = float64(got.(int64))
				case len(want.View) > 0:
					var wrapper any
					_ = json.Unmarshal(want.View, &wrapper)
					wantValue = dropEmptyToolLists(wrapper)
				}
				same = reflect.DeepEqual(canonical(got), wantValue)
			}
			if !same {
				differing++
				if differing <= 5 {
					gotJSON, _ := json.Marshal(got)
					progJSON, _ := json.Marshal(program[:j+1])
					t.Errorf("program %d step %d (%s %s)\n  program %.1500s\n  Pi  %.800s err %v\n  Pig %.800s err %v", i, j, step.Op, step.Mode, progJSON, want.View, ptrOutputText(want.Err), gotJSON, gotErr)
				}
			}
		}
	}
	t.Logf("%d programs, %d reads, %d appends with edits, %d with head, %d append errors, %d differing steps", len(programs), reads, withEdits, withHead, appendErrors, differing)
	if differing > 0 {
		t.Errorf("%d steps differ from Pi", differing)
	}
}
