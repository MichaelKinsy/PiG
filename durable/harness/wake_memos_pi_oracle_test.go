package harness

// pi: packages/durable/src/harness/scheduler.ts (reserved)

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/schema"
)

type wakeProbeInput struct {
	Sleep bool     `json:"sleep"`
	Keys  []string `json:"keys,omitempty"`
	Until *float64 `json:"until,omitempty"`
}

type (
	wakeProbeRuntime = durable.TaskRuntime[wakeProbeInput, stepState, bool, any]
	wakeProbeRecord  = durable.RunningTask[wakeProbeInput, stepState, bool]
)

// TestWakeRecordMemosMatchPi runs the same task over a few wakes on Pi and on Pig and compares the record the woken task
// reads of itself, as the JSON both persist: a wake writes the signal memos it marks, `{}` when it marks none, and a task
// that starts without a wake has no memos at all.
func TestWakeRecordMemosMatchPi(t *testing.T) {
	answer := durable.DefineSignal(durable.SignalDefinition[sigAnswer]{
		Name:    "test.answer",
		Version: 1,
		Schema:  schema.Obj(schema.Properties{schema.Prop("text", schema.Str())}),
	})
	scenario := func(t *testing.T, input wakeProbeInput, early *sigAnswer) any {
		t.Helper()
		var seen []byte
		probe := durable.DefineTask(durable.TaskDefinition[wakeProbeInput, stepState, bool, any]{
			Name:    "test.probe",
			Version: 1,
			Initial: func(input wakeProbeInput) stepState {
				if input.Sleep {
					return stepState{Phase: "ask"}
				}
				return stepState{Phase: "woke"}
			},
			Phases: map[string]durable.PhaseHandler[wakeProbeInput, stepState, bool, any]{
				"ask": func(ctx context.Context, task wakeProbeRecord, runtime wakeProbeRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, wakeProbeRecord) (*durable.NextTaskState[stepState, bool], error) {
						signals := make([]durable.SignalKey, len(task.Input.Keys))
						for index, key := range task.Input.Keys {
							signals[index] = answer.Key(key)
						}
						return &durable.NextTaskState[stepState, bool]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "woke"}, Signals: signals, Until: task.Input.Until}, nil
					})
				},
				"woke": func(ctx context.Context, _ wakeProbeRecord, runtime wakeProbeRuntime) error {
					record, err := runtime.GetTask(ctx, runtime.TaskId())
					if err != nil {
						return err
					}
					if seen, err = json.Marshal(record); err != nil {
						return err
					}
					return runtime.Commit(ctx, func(durable.Tx, wakeProbeRecord) (*durable.NextTaskState[stepState, bool], error) {
						return completed[stepState](true), nil
					})
				},
			},
		})
		opened := tkOpenRoot(t, []durable.AnyTask{probe.Erased()}, tkOptions{now: func() float64 { return 1_000 }})
		id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, probe, input, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		if early != nil {
			if _, err := durable.Commit(testContext, opened.harness, func(tx durable.Tx) (durable.SignalResult, error) {
				return durable.Signal(tx, id, answer, "a", *early)
			}); err != nil {
				t.Fatal(err)
			}
		}
		tkWaitOutcome(t, opened.harness, id)
		mustClose(t, opened.harness)
		var decoded any
		if err := json.Unmarshal(seen, &decoded); err != nil {
			t.Fatalf("record %q: %v", seen, err)
		}
		return decoded
	}

	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/wake_memos_oracle.mjs", suspendableOraclePackages(t))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected map[string]any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	until := 500.0
	cases := []struct {
		name  string
		input wakeProbeInput
		early *sigAnswer
	}{
		{"plain start", wakeProbeInput{}, nil},
		{"sleep until a time", wakeProbeInput{Sleep: true, Until: &until}, nil},
		{"sleep on a key until a time", wakeProbeInput{Sleep: true, Keys: []string{"a"}, Until: &until}, nil},
		{"sleep on a key with a value delivered", wakeProbeInput{Sleep: true, Keys: []string{"a"}}, &sigAnswer{Text: "early"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scenario(t, c.input, c.early)
			if !reflect.DeepEqual(got, expected[c.name]) {
				want, _ := json.Marshal(expected[c.name])
				have, _ := json.Marshal(got)
				t.Errorf("record of the woken task\n  Pi  %s\n  Pig %s", want, have)
			}
		})
	}
}
