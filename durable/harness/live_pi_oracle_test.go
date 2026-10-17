package harness

// pi: packages/durable/src/harness/live.ts (endRun, addCompactionStatus, compactionStatus, removeCompactionStatus, toolSlot, finishSlot, clearProgress), generation.ts (handOver)

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

type liveStep struct {
	Op         string         `json:"op"`
	Task       int            `json:"task,omitzero"`
	From       int            `json:"from,omitzero"`
	To         int            `json:"to,omitzero"`
	Entry      *int           `json:"entry,omitempty"`
	Attempt    int            `json:"attempt"`
	Status     map[string]any `json:"status,omitempty"`
	Settlement map[string]any `json:"settlement,omitempty"`
}

type liveProgram struct {
	Live  map[string]any `json:"live"`
	Steps []liveStep     `json:"steps"`
}

// liveSettleTx records the submissions a helper settles; the embedded Tx is never called otherwise.
type liveSettleTx struct {
	durable.Tx
	settled [][2]any
}

func (tx *liveSettleTx) SettleSubmission(id durable.SubmissionId, settlement durable.SubmissionSettlement) error {
	encoded, _ := json.Marshal(settlement)
	var decoded any
	_ = json.Unmarshal(encoded, &decoded)
	tx.settled = append(tx.settled, [2]any{float64(id), decoded})
	return nil
}

func livePrograms() []liveProgram {
	random := rand.New(rand.NewSource(20261113))
	pick := func(options ...any) any { return options[random.Intn(len(options))] }
	task := func() int { return random.Intn(4) + 1 }
	var programs []liveProgram
	for range 3000 {
		live := map[string]any{}
		if random.Intn(3) != 0 {
			live["run"] = map[string]any{"taskId": task(), "inputs": []any{random.Intn(3) + 1, random.Intn(3) + 4}}
		}
		if random.Intn(2) == 0 {
			live["generation"] = map[string]any{"attempt": 1}
		}
		if random.Intn(2) == 0 {
			var tools []any
			for range random.Intn(3) + 1 {
				slot := map[string]any{"callId": "c", "name": "bash", "status": pick("pending", "running"), "taskId": task()}
				if random.Intn(2) == 0 {
					slot["output"] = "o"
					slot["droppedBytes"] = 3
					slot["droppedLines"] = 1
				}
				if random.Intn(2) == 0 {
					slot["details"] = map[string]any{"k": 1}
					slot["diagnostics"] = []any{map[string]any{"severity": "warning", "message": "m"}}
				}
				tools = append(tools, slot)
			}
			live["tools"] = tools
		}
		if random.Intn(2) == 0 {
			var statuses []any
			for range random.Intn(3) + 1 {
				statuses = append(statuses, map[string]any{"taskId": task(), "reason": "threshold", "blocking": random.Intn(2) == 0, "attempt": 1})
			}
			live["compactions"] = statuses
		}
		program := liveProgram{Live: live}
		for range random.Intn(5) + 1 {
			step := liveStep{}
			switch random.Intn(7) {
			case 0:
				step = liveStep{Op: "endRun", Task: task(), Settlement: pick(map[string]any{"status": "done", "answer": random.Intn(5) + 1}, map[string]any{"status": "unanswered", "reason": "gone"}).(map[string]any)}
			case 1:
				step = liveStep{Op: "addCompaction", Status: map[string]any{"taskId": task(), "reason": "manual", "blocking": random.Intn(2) == 0, "attempt": 1}}
			case 2:
				step = liveStep{Op: "removeCompaction", Task: task()}
			case 3:
				step = liveStep{Op: "finishSlot", Task: task()}
				if random.Intn(2) == 0 {
					entry := random.Intn(9) + 1
					step.Entry = &entry
				}
			case 4:
				step = liveStep{Op: "clearProgress", Task: task()}
			case 5:
				step = liveStep{Op: "handOver", From: task(), To: task()}
			default:
				step = liveStep{Op: "setAttempt", Task: task(), Attempt: random.Intn(4)}
			}
			program.Steps = append(program.Steps, step)
		}
		programs = append(programs, program)
	}
	return programs
}

// TestLiveDocHelpersMatchPi runs Pi's pi.live draft helpers and Go's on a tracked document and compares the settled submissions, the published operations and the resulting value.
func TestLiveDocHelpersMatchPi(t *testing.T) {
	programs := livePrograms()
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/live_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Settled [][2]any `json:"settled"`
		Ops     []any    `json:"ops"`
		Value   any      `json:"value"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	differing, withOps := 0, 0
	for index, program := range programs {
		initial, err := chordjson.Decode(eventsJSON(t, program.Live))
		if err != nil {
			t.Fatal(err)
		}
		change := delta.Track(initial.(*delta.JsonObject)).BeginChange()
		live := change.State()
		tx := &liveSettleTx{}
		for _, step := range program.Steps {
			var err error
			switch step.Op {
			case "endRun":
				err = EndRun(tx, live, durable.TaskId(step.Task), durable.SubmissionSettlement{
					Status: durable.SubmissionStatus(step.Settlement["status"].(string)), Answer: durable.EntryId(intOf(step.Settlement["answer"])), Reason: stringOf(step.Settlement["reason"]),
				})
			case "addCompaction":
				err = AddCompactionStatus(live, CompactionStatus{TaskId: durable.TaskId(intOf(step.Status["taskId"])), Reason: durable.CompactionReason(step.Status["reason"].(string)), Blocking: step.Status["blocking"].(bool), Attempt: 1})
			case "removeCompaction":
				err = RemoveCompactionStatus(live, durable.TaskId(step.Task))
			case "finishSlot":
				// finishSlot, then the result entry as tool.ts:624-625 sets it.
				if slot := FindToolSlot(live, durable.TaskId(step.Task)); slot != nil {
					if err = FinishSlot(slot); err == nil && step.Entry != nil {
						err = slot.Set("entry", float64(*step.Entry))
					}
				}
			case "clearProgress":
				if slot := FindToolSlot(live, durable.TaskId(step.Task)); slot != nil {
					ClearProgress(slot)
				}
			case "handOver":
				err = handOver(live, durable.TaskId(step.From), durable.TaskId(step.To))
			case "setAttempt":
				if status := FindCompactionStatus(live, durable.TaskId(step.Task)); status != nil {
					err = status.Set("attempt", float64(step.Attempt))
				}
			}
			if err != nil {
				t.Fatalf("program %d %s: %v", index, step.Op, err)
			}
		}
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatal(err)
		}
		var gotOps []any
		_ = json.Unmarshal(eventsJSON(t, prepared.Ops()), &gotOps)
		var gotValue any
		_ = json.Unmarshal(eventsJSON(t, prepared.Value()), &gotValue)
		gotSettled := make([][2]any, 0, len(tx.settled))
		gotSettled = append(gotSettled, tx.settled...)
		want := expected[index]
		if len(want.Ops) > 0 {
			withOps++
		}
		if !reflect.DeepEqual(gotSettled, want.Settled) || !reflect.DeepEqual(append([]any{}, gotOps...), append([]any{}, want.Ops...)) || !reflect.DeepEqual(gotValue, want.Value) {
			differing++
			if differing <= 5 {
				t.Errorf("program %d: steps %s live %s\n got  settled %v ops %s value %s\n want settled %v ops %s value %s", index, eventsJSON(t, program.Steps), eventsJSON(t, program.Live), gotSettled, eventsJSON(t, gotOps), eventsJSON(t, gotValue), want.Settled, eventsJSON(t, want.Ops), eventsJSON(t, want.Value))
			}
		}
	}
	t.Logf("%d programs, %d with operations, %d differing", len(programs), withOps, differing)
	if differing > 0 {
		t.Fatalf("%d programs differ from Pi", differing)
	}
}

func intOf(value any) int {
	number, _ := value.(int)
	return number
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}
