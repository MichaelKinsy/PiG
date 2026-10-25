package harness

// pi: packages/durable/src/harness/task-graph.ts (TaskGraphView build and advance)

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// graphStep is one Session commit of a seeded program: "make" creates conversations and tasks, "advance" replaces one task's state.
type graphStep struct {
	Op      string    `json:"op"`
	Ops     []graphOp `json:"ops,omitempty"`
	Task    int       `json:"task"`
	To      string    `json:"to,omitempty"`
	Phase   string    `json:"phase,omitempty"`
	On      []int     `json:"on,omitempty"`
	Policy  string    `json:"policy,omitempty"`
	Outcome string    `json:"outcome,omitempty"`
	Abort   *bool     `json:"abort,omitempty"`
}

type graphOp struct {
	T          string `json:"t"`
	Owner      *int   `json:"owner,omitempty"`
	Conv       *int   `json:"conv,omitempty"`
	Background bool   `json:"background,omitempty"`
	// End, when set, moves the created task to this state in its creating commit: a terminal task never enters the graph, and a completing one enters it completing.
	End string `json:"end,omitempty"`
}

type graphDelivery struct {
	Ops json.RawMessage `json:"ops"`
}

type graphOutcome struct {
	Index int             `json:"index"`
	OK    json.RawMessage `json:"ok"`
	Err   string          `json:"err"`
	Live  json.RawMessage `json:"live"`
	Fresh json.RawMessage `json:"fresh"`
	// Deliveries is the operations of each frame the long-lived watch's listener received since the previous step.
	Deliveries []graphDelivery `json:"deliveries"`
}

var graphProbeTask = durable.DefineTask(durable.TaskDefinition[durable.JsonValue, durable.JsonValue, durable.JsonValue, struct{}]{
	Name:    "probe",
	Version: 1,
	Initial: func(input durable.JsonValue) durable.JsonValue {
		return map[string]any{"phase": "start", "input": input}
	},
})

func pickRef[T ~int64](list []T, ref int) T {
	if len(list) == 0 {
		return 1_000_000_000
	}
	return list[ref%len(list)]
}

func runPigTaskGraph(t *testing.T, program []graphStep) []graphOutcome {
	t.Helper()
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	line := session.CreateSession(store)
	view := NewTaskGraphView(line, store, TaskGraphBlocking{Blocked: func(BlockedSubject) durable.BlockedReason { return "" }, SubscribeBlocked: func(func()) func() { return func() {} }})
	live, err := view.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var deliveries []graphDelivery
	live.Start(func(_ context.Context, _ TaskGraph, ops []durable.Op) error {
		encoded, err := json.Marshal(ops)
		if err != nil {
			return err
		}
		mu.Lock()
		deliveries = append(deliveries, graphDelivery{Ops: encoded})
		mu.Unlock()
		return nil
	})
	var convs []durable.ConversationId
	var tasks []durable.TaskId
	var outcomes []graphOutcome
	for index, step := range program {
		var madeConvs []durable.ConversationId
		var madeTasks []durable.TaskId
		value, err := line.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
			if step.Op == "advance" {
				id := pickRef(tasks, step.Task)
				record, err := tx.Task(id)
				if err != nil {
					return nil, err
				}
				if record == nil {
					return nil, fmt.Errorf("no task %d", id)
				}
				checkpoint := durable.JsonValue(map[string]any{"phase": step.Phase})
				outcome := &durable.TaskOutcome[durable.JsonValue]{Status: durable.TaskOutcomeStatus(step.Outcome), Result: new(durable.JsonValue(float64(1)))}
				state := durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskStatus(step.To)}
				switch step.To {
				case "pending", "running":
					state.Checkpoint = &checkpoint
				case "waiting":
					state.Checkpoint = &checkpoint
					state.Policy = durable.JoinPolicy(step.Policy)
					state.On = []durable.TaskId{}
					for _, ref := range step.On {
						state.On = append(state.On, pickRef(tasks, ref))
					}
				default:
					state.Outcome = outcome
				}
				// The returned record is Session-owned and immutable: replace a copy, as upstream spreads it.
				replaced := *record
				if step.Abort != nil {
					replaced.AbortRequested = *step.Abort
				}
				replaced.State = state
				return nil, tx.SetTask(replaced)
			}
			var out []int64
			for _, op := range step.Ops {
				if op.T == "task" {
					ownership := durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}
					if op.Owner != nil {
						ownership = durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: pickRef(append(append([]durable.TaskId(nil), tasks...), madeTasks...), *op.Owner)}
					}
					options := durable.TaskOptions{Ownership: ownership, Background: op.Background}
					if op.Conv != nil {
						options.ConversationId = new(pickRef(append(append([]durable.ConversationId(nil), convs...), madeConvs...), *op.Conv))
					}
					id, err := tx.CreateTaskErased(graphProbeTask.Erased(), float64(1), options)
					if err != nil {
						return nil, err
					}
					madeTasks = append(madeTasks, id)
					out = append(out, int64(id))
					if op.End != "" {
						// tx.Task cannot read after the commit's first table write, so the record is written whole, as the create made it.
						record := durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{Id: id, ConversationId: *options.ConversationId, Kind: "probe", Version: 1, Input: durable.JsonValue(float64(1)), Background: op.Background}
						record.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskStatus(op.End), Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: new(durable.JsonValue(float64(1)))}}
						if err := tx.SetTask(record); err != nil {
							return nil, err
						}
					}
					continue
				}
				ownership := durable.ConversationOwnership{Kind: durable.ConversationOwnerless}
				if op.Owner != nil {
					ownership = durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: pickRef(append(append([]durable.TaskId(nil), tasks...), madeTasks...), *op.Owner)}
				}
				record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: ownership})
				if err != nil {
					return nil, err
				}
				madeConvs = append(madeConvs, record.Id)
				out = append(out, int64(record.Id))
			}
			return out, nil
		}, session.TransactionScope{})
		outcome := graphOutcome{Index: index}
		if err != nil {
			outcome.Err = err.Error()
		} else {
			convs, tasks = append(convs, madeConvs...), append(tasks, madeTasks...)
			encoded, _ := json.Marshal(value)
			outcome.OK = encoded
		}
		line.WaitDeliveries()
		fresh, err := NewTaskGraphView(line, store, TaskGraphBlocking{Blocked: func(BlockedSubject) durable.BlockedReason { return "" }, SubscribeBlocked: func(func()) func() { return func() {} }}).Watch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Live, err = json.Marshal(live.Value()); err != nil {
			t.Fatal(err)
		}
		if outcome.Fresh, err = json.Marshal(fresh.Value()); err != nil {
			t.Fatal(err)
		}
		_, _ = fresh.Stop()
		mu.Lock()
		outcome.Deliveries, deliveries = deliveries, nil
		mu.Unlock()
		outcomes = append(outcomes, outcome)
	}
	_, _ = live.Stop()
	if err := line.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	return outcomes
}

func graphProgramFor(seed int64) []graphStep {
	random := rand.New(rand.NewSource(seed))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	ref := func(n int) *int { v := random.Intn(n); return &v }
	var program []graphStep
	program = append(program, graphStep{Op: "make", Ops: []graphOp{{T: "conv"}, {T: "task", Conv: ref(1)}}})
	for range 22 {
		switch random.Intn(5) {
		case 0, 1:
			step := graphStep{Op: "make"}
			for range 1 + random.Intn(3) {
				op := graphOp{T: pick("conv", "task", "task")}
				if random.Intn(3) == 0 {
					op.Owner = ref(6)
				}
				if op.T == "task" {
					if op.Owner == nil || random.Intn(4) == 0 {
						op.Conv = ref(6)
					}
					op.Background = random.Intn(6) == 0
					// A conversation-owned task in a named conversation can end in the commit that creates it.
					if op.Owner == nil && op.Conv != nil && random.Intn(4) == 0 {
						op.End = pick("terminal", "completing")
					}
				}
				step.Ops = append(step.Ops, op)
			}
			program = append(program, step)
		default:
			step := graphStep{Op: "advance", Task: random.Intn(6), To: pick("pending", "running", "waiting", "completing", "terminal"), Phase: pick("a", "b", "c"), Policy: pick("failFast", "allSettled"), Outcome: pick("completed", "failed", "aborted")}
			for range random.Intn(3) {
				step.On = append(step.On, random.Intn(6))
			}
			if random.Intn(5) == 0 {
				abort := random.Intn(2) == 0
				step.Abort = &abort
			}
			program = append(program, step)
		}
	}
	return program
}

func TestTaskGraphViewMatchesPiOnSeededPrograms(t *testing.T) {
	const seeded = 300
	programs := make([]map[string]any, seeded)
	steps := make([][]graphStep, seeded)
	for i := range programs {
		steps[i] = graphProgramFor(int64(4440000 + i))
		programs[i] = map[string]any{"steps": steps[i]}
	}
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/taskgraph_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]graphOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	canonical := func(raw json.RawMessage) any {
		var value any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatalf("%s: %v", raw, err)
			}
		}
		return value
	}
	differing, refused, owned, waiting, completing, dropped := 0, 0, 0, 0, 0, 0
	ended := 0
	for i, program := range steps {
		got := runPigTaskGraph(t, program)
		for k, want := range expected[i] {
			if got[k].Err != want.Err || !reflect.DeepEqual(canonical(got[k].OK), canonical(want.OK)) || !reflect.DeepEqual(canonical(got[k].Live), canonical(want.Live)) || !reflect.DeepEqual(canonical(got[k].Fresh), canonical(want.Fresh)) || !sameDeliveries(got[k].Deliveries, want.Deliveries) {
				differing++
				if differing <= 3 {
					t.Errorf("program %d step %d (%s)\n  Pi  deliveries=%v err=%q ok=%s\n  Pig deliveries=%v err=%q ok=%s\n  %s", i, k, program[k].Op, want.Deliveries, want.Err, want.OK, got[k].Deliveries, got[k].Err, got[k].OK, graphDiff(canonical(want.Live), canonical(got[k].Live), "live")+graphDiff(canonical(want.Fresh), canonical(got[k].Fresh), "fresh"))
				}
			}
			if want.Err != "" {
				refused++
			}
			for _, op := range program[k].Ops {
				if op.End != "" && want.Err == "" {
					ended++
				}
			}
			if bytes.Contains(want.Live, []byte(`"conversations":[`)) && !bytes.Contains(want.Live, []byte(`"conversations":[]`)) {
				owned++
			}
			if bytes.Contains(want.Live, []byte(`"status":"waiting"`)) {
				waiting++
			}
			if bytes.Contains(want.Live, []byte(`"status":"completing"`)) {
				completing++
			}
			if k > 0 && len(want.Live) < len(expected[i][k-1].Live) {
				dropped++
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d answers differ from Pi", differing)
	}
	t.Logf("%d refusals, %d states with owned conversations, %d waiting, %d completing, %d graph shrinks, %d tasks ended in their creating commit", refused, owned, waiting, completing, dropped, ended)
	if refused < 50 || owned < 50 || waiting < 50 || completing < 20 || dropped < 20 || ended < 50 {
		t.Errorf("probes are one-sided")
	}
}

// graphDiff names the tasks whose nodes differ between two graph values.
func graphDiff(pi, pig any, label string) string {
	piTasks, _ := pi.(map[string]any)["tasks"].(map[string]any)
	pigTasks, _ := pig.(map[string]any)["tasks"].(map[string]any)
	out := ""
	for key, node := range piTasks {
		if !reflect.DeepEqual(node, pigTasks[key]) {
			a, _ := json.Marshal(node)
			b, _ := json.Marshal(pigTasks[key])
			out += fmt.Sprintf("%s task %s: Pi %.200s | Pig %.200s; ", label, key, a, b)
		}
	}
	for key := range pigTasks {
		if _, ok := piTasks[key]; !ok {
			out += fmt.Sprintf("%s task %s only in Pig; ", label, key)
		}
	}
	return out
}

func sameDeliveries(a, b []graphDelivery) bool {
	decode := func(deliveries []graphDelivery) []any {
		decoded := make([]any, 0, len(deliveries))
		for _, delivery := range deliveries {
			var ops any
			if err := json.Unmarshal(delivery.Ops, &ops); err != nil {
				return nil
			}
			decoded = append(decoded, ops)
		}
		return decoded
	}
	return len(a) == 0 && len(b) == 0 || reflect.DeepEqual(decode(a), decode(b))
}
