package harness

// pi: packages/durable/src/harness/tool.ts (checkKey), packages/durable/src/harness/scheduler.ts (ownedTasks)

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A nested call key's error quotes the key as JSON.stringify does (tool.ts checkKey): a lone surrogate is its escape, and <, & and > stay.
func TestANestedKeyErrorQuotesTheKeyAsPiDoes(t *testing.T) {
	keys := []struct{ pi, key string }{
		{`"x/\ud800"`, "x/\xed\xa0\x80"},
		{`"x/<&>"`, "x/<&>"},
		{`"a\u2028b/"`, "a\u2028b/"},
		{`"\udc00/\ud800"`, "\xed\xb0\x80/\xed\xa0\x80"},
		{`"7"`, "7"},
	}
	// The oracle's stdin is JSON.parse'd, so a lone surrogate reaches it as its escape: send the keys as text.
	var payload bytes.Buffer
	payload.WriteByte('[')
	for index, k := range keys {
		if index > 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`{"op":"checkKey","key":` + k.pi + `}`)
	}
	payload.WriteByte(']')
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), testenv.RequireQualifiedNode(t), "--disable-warning=ExperimentalWarning", "testdata/tool_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = &payload
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi []struct {
		Err *string `json:"err"`
	}
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(keys) {
		t.Fatalf("oracle output %s: %v", output, err)
	}
	for index, k := range keys {
		got := checkNestedKey(k.key)
		switch {
		case pi[index].Err == nil && got != nil:
			t.Errorf("key %s: error %q, Pi accepts it", k.pi, got)
		case pi[index].Err != nil && got == nil:
			t.Errorf("key %s accepted, Pi says %q", k.pi, *pi[index].Err)
		case pi[index].Err != nil && got.Error() != *pi[index].Err:
			t.Errorf("key %s:\n got %s\nwant %s (Pi)", k.pi, got, *pi[index].Err)
		}
	}
}

type surrogateInput struct {
	Name string `json:"name"`
}

type surrogateState struct {
	Phase string `json:"phase"`
	Note  string `json:"note"`
}

// ownedTasks hands a task its owned tasks as they are stored: the units of a lone surrogate in a kind, an input and a checkpoint stay.
func TestOwnedTasksKeepsALoneSurrogateInTheRecordsItReturns(t *testing.T) {
	const kind = "k\xed\xa0\x80"
	const name = "n\xed\xa0\x81"
	const note = "c\xed\xb0\x80"
	type childRecord = durable.RunningTask[surrogateInput, surrogateState, durable.JsonValue]
	type childRuntime = durable.TaskRuntime[surrogateInput, surrogateState, durable.JsonValue, any]
	child := durable.DefineTask(durable.TaskDefinition[surrogateInput, surrogateState, durable.JsonValue, any]{
		Name:    kind,
		Version: 1,
		Initial: func(surrogateInput) surrogateState { return surrogateState{Phase: "hold", Note: note} },
		Phases: map[string]durable.PhaseHandler[surrogateInput, surrogateState, durable.JsonValue, any]{
			"hold": func(_ context.Context, _ childRecord, runtime childRuntime) error {
				<-runtime.Signal().Done()
				return context.Cause(runtime.Signal())
			},
		},
		Abort: func(ctx context.Context, _ childRecord, runtime childRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, childRecord) (*durable.NextTaskState[surrogateState, durable.JsonValue], error) {
				return abortedWith[surrogateState, durable.JsonValue](""), nil
			})
		},
	})
	owned := make(chan []anyTaskRecord, 1)
	owner := durable.DefineTask(durable.TaskDefinition[probeInput, probeState, durable.JsonValue, any]{
		Name:    "test.surrogate.owner",
		Version: 1,
		Initial: func(probeInput) probeState { return probeState{Phase: "look"} },
		Phases: map[string]durable.PhaseHandler[probeInput, probeState, durable.JsonValue, any]{
			"look": func(ctx context.Context, _ probeRecord, runtime probeRuntime) error {
				records, err := runtime.OwnedTasks(ctx)
				if err != nil {
					return err
				}
				owned <- records
				return runtime.Commit(ctx, func(durable.Tx, probeRecord) (*durable.NextTaskState[probeState, durable.JsonValue], error) {
					return completed[probeState, durable.JsonValue](nil), nil
				})
			},
		},
	})
	harness, _, _ := openTasks(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")), []durable.AnyTask{child.Erased(), owner.Erased()})
	root := mustRoot(t, harness, nil)
	must(durable.Commit(testContext, root, func(tx durable.Tx) (struct{}, error) {
		id, err := durable.CreateTask(tx, owner, probeInput{Name: "owner"}, durable.TaskOptions{Ownership: conversationOwned})
		if err != nil {
			return struct{}{}, err
		}
		_, err = durable.CreateTask(tx, child, surrogateInput{Name: name}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: id}})
		return struct{}{}, err
	}))
	harness.Resume()
	records := <-owned
	if len(records) != 1 {
		t.Fatalf("ownedTasks = %d records, want the child", len(records))
	}
	record := records[0]
	if record.State.Checkpoint == nil {
		t.Fatalf("record = %+v, want a checkpoint", record)
	}
	gotName, gotNote := member(record.Input, "name"), member(*record.State.Checkpoint, "note")
	if record.Kind != kind || gotName != name || gotNote != note {
		t.Errorf("ownedTasks returned kind %q, input name %q, checkpoint note %q; want %q, %q, %q", record.Kind, gotName, gotNote, kind, name, note)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// member is the value of one key of a decoded JSON object, whatever object type the decoder gave.
func member(object durable.JsonValue, key string) any {
	switch typed := object.(type) {
	case durable.JsonObject:
		value, _ := typed.Get(key)
		return value
	case map[string]any:
		return typed[key]
	}
	return nil
}
