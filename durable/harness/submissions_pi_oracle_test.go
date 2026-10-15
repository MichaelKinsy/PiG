package harness

// pi: packages/durable/src/harness/submissions.ts (admitSubmission) and inbox.ts (prepareBoundary, applyBoundary, withdrawQueuedInputs)

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// admitStep is one commit of a seeded program: a Session commit that creates a conversation, appends an entry, admits a submission, ends a run, applies an inbox boundary or withdraws queued inputs.
type admitStep struct {
	Op    string          `json:"op"`
	Conv  int             `json:"conv"`
	Now   float64         `json:"now,omitempty"`
	At    string          `json:"at,omitempty"`
	Modes *admitModes     `json:"modes,omitempty"`
	Draft json.RawMessage `json:"draft,omitempty"`
}

type admitModes struct {
	SteeringMode durable.QueueMode `json:"steeringMode"`
	FollowUpMode durable.QueueMode `json:"followUpMode"`
}

type admitDraft struct {
	RequestID *string         `json:"requestId"`
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	WhenBusy  string          `json:"whenBusy"`
	Entry     *admitEntry     `json:"entry"`
}

type admitEntry struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
	Head json.RawMessage `json:"head"`
}

func pickConv(list []int64, ref int) int64 {
	if len(list) == 0 {
		return 1_000_000_000
	}
	return list[ref%len(list)]
}

func (e admitEntry) draft(t *testing.T, entries []int64) durable.EntryDraft {
	draft := durable.EntryDraft{Kind: e.Kind}
	if len(e.Data) > 0 {
		if err := json.Unmarshal(e.Data, &draft.Data); err != nil {
			t.Fatal(err)
		}
	}
	switch {
	case len(e.Head) == 0:
	case string(e.Head) == `"self"`:
		draft.HeadSelf = true
	default:
		var ref struct{ Ref int }
		if err := json.Unmarshal(e.Head, &struct {
			Ref *int `json:"ref"`
		}{&ref.Ref}); err != nil {
			t.Fatal(err)
		}
		draft.Head = new(durable.EntryId(pickConv(entries, ref.Ref)))
	}
	return draft
}

func (d admitDraft) submission(t *testing.T, entries []int64) durable.SubmissionDraft {
	draft := durable.SubmissionDraft{RequestId: d.RequestID, Type: durable.SubmissionType(d.Type), WhenBusy: durable.WhenBusy(d.WhenBusy)}
	if len(d.Content) > 0 {
		var text string
		if json.Unmarshal(d.Content, &text) == nil {
			draft.Content = ai.UserText(text)
		} else {
			var blocks []ai.TextContent
			if err := json.Unmarshal(d.Content, &blocks); err != nil {
				t.Fatal(err)
			}
			content := make(ai.UserContentBlocks, len(blocks))
			for i := range blocks {
				content[i] = blocks[i]
			}
			draft.Content = content
		}
	}
	if d.Entry != nil && d.Type == "input" {
		inputEntry := durable.InputEntry{Kind: d.Entry.Kind}
		if len(d.Entry.Data) > 0 {
			if err := json.Unmarshal(d.Entry.Data, &inputEntry.Data); err != nil {
				t.Fatal(err)
			}
		}
		draft.InputEntry = &inputEntry
	} else if d.Entry != nil {
		entry := d.Entry.draft(t, entries)
		draft.Entry = &entry
	}
	return draft
}

type admitOutcome struct {
	Index int             `json:"index"`
	OK    json.RawMessage `json:"ok"`
	Undef bool            `json:"undef"`
	Err   string          `json:"err"`
	State json.RawMessage `json:"state"`
}

func runPigAdmission(t *testing.T, program []admitStep) []admitOutcome {
	t.Helper()
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	line := session.CreateSession(store)
	var convs, entries []int64
	var outcomes []admitOutcome
	scanAll := func(id int64) []durable.EntryRecord {
		var all []durable.EntryRecord
		var cursor durable.Cursor
		for {
			page, err := store.ScanEntries(ctx, durable.EntryQuery{ConversationId: durable.ConversationId(id)}, 1000, cursor)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, page.Items...)
			if page.Next == nil {
				return all
			}
			cursor = *page.Next
		}
	}
	for index, step := range program {
		conv := durable.ConversationId(pickConv(convs, step.Conv))
		var value any
		var err error
		switch step.Op {
		case "create":
			value, err = line.Commit(ctx, func(tx durable.Tx) (any, error) {
				return tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
			})
		case "append":
			var draft admitEntry
			if err := json.Unmarshal(step.Draft, &draft); err != nil {
				t.Fatal(err)
			}
			value, err = line.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.AppendEntry(conv, draft.draft(t, entries)) })
		case "admit":
			var draft admitDraft
			if err := json.Unmarshal(step.Draft, &draft); err != nil {
				t.Fatal(err)
			}
			modes := QueueModes{SteeringMode: step.Modes.SteeringMode, FollowUpMode: step.Modes.FollowUpMode}
			value, err = line.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
				return AdmitSubmission(tx, conv, draft.submission(t, entries), step.Now, modes)
			}, session.TransactionScope{})
		case "endRun":
			_, err = line.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
				live, err := tx.Doc(LiveDoc, conv)
				if err != nil {
					return nil, err
				}
				live.Delete("run")
				return nil, nil
			}, session.TransactionScope{})
		case "boundary":
			modes := QueueModes{SteeringMode: step.Modes.SteeringMode, FollowUpMode: step.Modes.FollowUpMode}
			value, err = line.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
				boundary, err := PrepareBoundary(tx, conv, modes)
				if err != nil {
					return nil, err
				}
				result, err := ApplyBoundary(tx, boundary, BoundaryAt(step.At), step.Now)
				if err != nil {
					return nil, err
				}
				users := result.Users
				if users == nil {
					users = []durable.SubmissionId{}
				}
				return map[string]any{"users": users, "reset": result.Reset}, nil
			}, session.TransactionScope{})
		case "withdraw":
			_, err = line.CommitWith(ctx, func(tx *session.Transaction) (any, error) { return nil, WithdrawQueuedInputs(tx, conv) }, session.TransactionScope{})
		default:
			t.Fatalf("unknown op %s", step.Op)
		}
		outcome := admitOutcome{Index: index}
		switch {
		case err != nil:
			outcome.Err = err.Error()
		case value == nil:
			outcome.Undef = true
		default:
			encoded, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			outcome.OK = encoded
		}
		if step.Op == "create" && err == nil {
			convs = append(convs, int64(value.(durable.ConversationRecord).Id))
		}
		entries = entries[:0]
		for _, id := range convs {
			for _, entry := range scanAll(id) {
				entries = append(entries, int64(entry.Id))
			}
		}
		outcomes = append(outcomes, outcome)
	}
	state := map[string]any{"entries": []durable.EntryRecord{}, "submissions": []durable.SubmissionRecord{}, "docs": []any{}, "values": []any{}, "tasks": []any{}}
	for _, id := range convs {
		state["entries"] = append(state["entries"].([]durable.EntryRecord), scanAll(id)...)
		page, err := store.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ConversationId(id)}, At: durable.CurrentPoint}, 1000, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, doc := range page.Items {
			state["docs"] = append(state["docs"].([]any), doc)
			stored, err := store.Document(ctx, doc.Id, durable.CurrentPoint)
			if err != nil || stored == nil {
				t.Fatalf("document %d: %v", doc.Id, err)
			}
			state["values"] = append(state["values"].([]any), map[string]any{"id": doc.Id, "kind": doc.Kind, "version": stored.Version, "value": stored.Value})
		}
	}
	for id := int64(1); id <= 80; id++ {
		if submission, err := store.Submission(ctx, durable.SubmissionId(id)); err != nil {
			t.Fatal(err)
		} else if submission != nil {
			state["submissions"] = append(state["submissions"].([]durable.SubmissionRecord), *submission)
		}
		if task, err := store.Task(ctx, durable.TaskId(id)); err != nil {
			t.Fatal(err)
		} else if task != nil {
			state["tasks"] = append(state["tasks"].([]any), task)
		}
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	outcomes = append(outcomes, admitOutcome{State: encoded})
	if err := line.Close(ctx); err != nil {
		t.Fatal(err)
	}
	return outcomes
}

func canonicalAdmission(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return value
}

func admissionProgramFor(seed int64) []admitStep {
	random := rand.New(rand.NewSource(seed))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	modes := func() *admitModes {
		return &admitModes{SteeringMode: durable.QueueMode(pick("all", "one-at-a-time")), FollowUpMode: durable.QueueMode(pick("all", "one-at-a-time"))}
	}
	head := func() string {
		switch random.Intn(6) {
		case 0:
			return `,"head":"self"`
		case 1, 2:
			return fmt.Sprintf(`,"head":{"ref":%d}`, random.Intn(20))
		}
		return ""
	}
	entry := func() string {
		return fmt.Sprintf(`{"kind":%q%s%s}`, pick("note", "marker"), pick(``, `,"data":1`, `,"data":{"a":[1]}`), head())
	}
	content := func() string {
		return pick(`"hi"`, `"yo"`, `[{"type":"text","text":"blocks"}]`, `""`)
	}
	admit := func() admitStep {
		var parts []string
		if random.Intn(3) == 0 {
			parts = append(parts, fmt.Sprintf(`"requestId":%q`, pick("r1", "r2", "r3")))
		}
		if random.Intn(4) == 0 {
			parts = append(parts, `"type":"write"`, `"entry":`+entry())
			if random.Intn(3) == 0 {
				parts = append(parts, `"whenBusy":"reject"`) // Pi's type forbids it on a write; at runtime only an input honours it
			}
		} else {
			parts = append(parts, `"type":"input"`, `"content":`+content())
			if w := pick("", "", "steer", "followUp", "reject"); w != "" {
				parts = append(parts, fmt.Sprintf(`"whenBusy":%q`, w))
			}
		}
		draft := "{"
		for i, part := range parts {
			if i > 0 {
				draft += ","
			}
			draft += part
		}
		return admitStep{Op: "admit", Conv: random.Intn(3), Now: float64(1 + random.Intn(9)), Modes: modes(), Draft: json.RawMessage(draft + "}")}
	}
	program := []admitStep{{Op: "create"}, {Op: "create"}}
	for range 24 {
		switch random.Intn(12) {
		case 0:
			program = append(program, admitStep{Op: "endRun", Conv: random.Intn(3)})
		case 1, 2:
			program = append(program, admitStep{Op: "boundary", Conv: random.Intn(3), At: pick("postTools", "final"), Now: float64(1 + random.Intn(9)), Modes: modes()})
		case 3:
			program = append(program, admitStep{Op: "withdraw", Conv: random.Intn(3)})
		case 4:
			program = append(program, admitStep{Op: "append", Conv: random.Intn(3), Draft: json.RawMessage(entry())})
		default:
			program = append(program, admit())
		}
	}
	return program
}

// piAdmissionOutcomes runs the programs through Pi's admission code and returns each program's step outcomes and final state.
func piAdmissionOutcomes(t *testing.T, programs [][]admitStep) [][]admitOutcome {
	t.Helper()
	wrapped := make([]map[string]any, len(programs))
	for i, program := range programs {
		wrapped[i] = map[string]any{"steps": program}
	}
	payload, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/submissions_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]admitOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	return expected
}

func TestAdmitSubmissionMatchesPiOnSeededPrograms(t *testing.T) {
	const seeded = 300
	programs := make([][]admitStep, seeded)
	for i := range programs {
		programs[i] = admissionProgramFor(int64(7770000 + i))
	}
	expected := piAdmissionOutcomes(t, programs)
	differing, refused, placed, queued, busy := 0, 0, 0, 0, 0
	seen := map[string]int{}
	for i, program := range programs {
		got := runPigAdmission(t, program)
		if len(got) != len(expected[i]) {
			t.Fatalf("program %d: %d outcomes, Pi %d", i, len(got), len(expected[i]))
		}
		for k := range got {
			want := expected[i][k]
			if got[k].Err != want.Err || got[k].Undef != want.Undef ||
				!reflect.DeepEqual(canonicalAdmission(t, got[k].OK), canonicalAdmission(t, want.OK)) ||
				!reflect.DeepEqual(canonicalAdmission(t, got[k].State), canonicalAdmission(t, want.State)) {
				differing++
				if differing <= 4 {
					t.Errorf("program %d step %d (%s)\n  Pi  %s\n  Pig %s", i, k, stepName(program, k), brief(t, want, got[k]), brief(t, got[k], want))
				}
			}
			if want.Err != "" {
				refused++
				if strings.HasSuffix(want.Err, "is busy") {
					busy++
				}
			}
			for _, marker := range []string{`"reason":"stale"`, `"reason":"aborted"`, `"type":"write","status":"done"`, `"type":"write","status":"queued"`, `already identifies`} {
				if bytes.Contains(want.State, []byte(marker)) || strings.Contains(want.Err, marker) {
					seen[marker]++
				}
			}
			if bytes.Contains(want.State, []byte(`"status":"placed"`)) {
				placed++
			}
			if bytes.Contains(want.State, []byte(`"status":"queued"`)) {
				queued++
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d answers differ from Pi", differing)
	}
	t.Logf("%d refusals (%d busy); final states with placed %d, queued %d", refused, busy, placed, queued)
	t.Logf("%v", seen)
	if seen[`"reason":"stale"`] < 5 || seen[`"reason":"aborted"`] < 50 || seen[`already identifies`] < 5 || seen[`"type":"write","status":"done"`] < 50 || seen[`"type":"write","status":"queued"`] < 20 {
		t.Errorf("probes miss a case: %v", seen)
	}
	if refused < 100 || busy < 20 || placed < 100 || queued < 50 {
		t.Errorf("probes are one-sided: %d refusals, %d busy, %d placed, %d queued", refused, busy, placed, queued)
	}
}

func stepName(program []admitStep, k int) string {
	if k >= len(program) {
		return "final state"
	}
	return program[k].Op
}

// brief shows the members of a that differ from b, truncated.
func brief(t *testing.T, a, b admitOutcome) string {
	t.Helper()
	out := fmt.Sprintf("undef=%v err=%q", a.Undef, a.Err)
	if !reflect.DeepEqual(canonicalAdmission(t, a.OK), canonicalAdmission(t, b.OK)) {
		out += fmt.Sprintf(" ok=%.300s", a.OK)
	}
	if am, ok := canonicalAdmission(t, a.State).(map[string]any); ok {
		bm, _ := canonicalAdmission(t, b.State).(map[string]any)
		for key, value := range am {
			if !reflect.DeepEqual(value, bm[key]) {
				encoded, _ := json.Marshal(value)
				out += fmt.Sprintf(" state.%s=%.700s", key, encoded)
			}
		}
	}
	return out
}

// inputEntryProgramFor is a seeded program of inputs placed as application entries (submissions.ts InputEntry): idle and busy
// admission, queued inputs placed by steer, follow-up, and final boundaries, withdrawal, and kinds the check rejects.
// It holds no writes.
func inputEntryProgramFor(seed int64) []admitStep {
	random := rand.New(rand.NewSource(seed))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	modes := func() *admitModes {
		return &admitModes{SteeringMode: durable.QueueMode(pick("all", "one-at-a-time")), FollowUpMode: durable.QueueMode(pick("all", "one-at-a-time"))}
	}
	admit := func() admitStep {
		parts := []string{`"type":"input"`, `"content":` + pick(`"hi"`, `"yo"`, `[{"type":"text","text":"blocks"}]`, `""`)}
		if random.Intn(3) == 0 {
			parts = append(parts, fmt.Sprintf(`"requestId":%q`, pick("r1", "r2", "r3")))
		}
		if w := pick("", "", "steer", "followUp", "reject"); w != "" {
			parts = append(parts, fmt.Sprintf(`"whenBusy":%q`, w))
		}
		if random.Intn(5) != 0 {
			kind := pick("note", "author.msg", "note", "pi.user", "pi.custom", "")
			parts = append(parts, fmt.Sprintf(`"entry":{"kind":%q%s}`, kind, pick(``, `,"data":1`, `,"data":{"author":"ada","tags":[1,"x"]}`, `,"data":"who"`)))
		}
		return admitStep{Op: "admit", Conv: random.Intn(3), Now: float64(1 + random.Intn(9)), Modes: modes(), Draft: json.RawMessage("{" + strings.Join(parts, ",") + "}")}
	}
	program := []admitStep{{Op: "create"}, {Op: "create"}}
	for range 24 {
		switch random.Intn(10) {
		case 0:
			program = append(program, admitStep{Op: "endRun", Conv: random.Intn(3)})
		case 1, 2:
			program = append(program, admitStep{Op: "boundary", Conv: random.Intn(3), At: pick("postTools", "final"), Now: float64(1 + random.Intn(9)), Modes: modes()})
		case 3:
			program = append(program, admitStep{Op: "withdraw", Conv: random.Intn(3)})
		default:
			program = append(program, admit())
		}
	}
	return program
}

// TestAdmitInputEntryMatchesPiOnSeededPrograms drives Pi's admitSubmission and applyBoundary and Pig's over inputs that carry
// an InputEntry: the entry kind and data a placed input is stored with, the queued inbox item, and the rejection of a kind
// that is empty or starts with "pi." must equal Pi's.
func TestAdmitInputEntryMatchesPiOnSeededPrograms(t *testing.T) {
	const seeded = 200
	programs := make([][]admitStep, seeded)
	for i := range programs {
		programs[i] = inputEntryProgramFor(int64(8880000 + i))
	}
	expected := piAdmissionOutcomes(t, programs)
	seen := map[string]int{}
	differing := 0
	for i, program := range programs {
		got := runPigAdmission(t, program)
		if len(got) != len(expected[i]) {
			t.Fatalf("program %d: %d outcomes, Pi %d", i, len(got), len(expected[i]))
		}
		for k := range got {
			want := expected[i][k]
			if got[k].Err != want.Err || got[k].Undef != want.Undef ||
				!reflect.DeepEqual(canonicalAdmission(t, got[k].OK), canonicalAdmission(t, want.OK)) ||
				!reflect.DeepEqual(canonicalAdmission(t, got[k].State), canonicalAdmission(t, want.State)) {
				differing++
				if differing <= 4 {
					t.Errorf("program %d step %d (%s)\n  Pi  %s\n  Pig %s", i, k, stepName(program, k), brief(t, want, got[k]), brief(t, got[k], want))
				}
			}
			if strings.Contains(want.Err, "An input entry's kind must be") {
				seen["rejected"]++
			}
			for _, marker := range []string{`"kind":"note"`, `"kind":"author.msg"`, `"data":{"author":"ada","tags":[1,"x"]}`, `"mode":"followUp","content":`, `"status":"queued"`} {
				if bytes.Contains(want.State, []byte(marker)) {
					seen[marker]++
				}
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d answers differ from Pi", differing)
	}
	for _, marker := range []string{"rejected", `"kind":"note"`, `"kind":"author.msg"`, `"data":{"author":"ada","tags":[1,"x"]}`, `"status":"queued"`} {
		if seen[marker] < 20 {
			t.Errorf("probes miss %s: %v", marker, seen)
		}
	}
}
