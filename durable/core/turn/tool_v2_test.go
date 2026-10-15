package turn_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// Pi 42a3497d harness/tool.ts: pi.tool is version 2, its input is `{ kind: "model", assistant, callId }` and its result
// `{ kind: "model", entryId, control? }`.
func TestToolTaskRecordIsAVersion2ModelCall(t *testing.T) {
	r := newRig(t)
	r.toolRound(`{"text":"x"}`)
	r.toolRuns[0].then(mustParse(t, `{"output":[{"type":"text","text":"ok"}]}`), nil)
	r.run()
	tools := r.tasks(turn.KindTool)
	if len(tools) != 1 {
		t.Fatalf("tool tasks = %d", len(tools))
	}
	if version, _ := tools[0].Get("version"); version != float64(2) {
		t.Errorf("pi.tool version = %v, want 2", version)
	}
	input, _ := tools[0].Get("input")
	if got := jv.Stringify(input); !strings.HasPrefix(got, `{"kind":"model","assistant":`) || !strings.HasSuffix(got, `"callId":"call_1"}`) {
		t.Errorf("input = %s", got)
	}
	if got := outcomeOf(t, tools[0]); !strings.HasPrefix(got, `{"status":"completed","result":{"kind":"model","entryId":`) {
		t.Errorf("outcome = %s", got)
	}
}

// A version 1 task migrates to `{ kind: "model", ...input }` with its checkpoint unchanged (tool.ts migrate).
func TestToolTaskMigratesVersion1(t *testing.T) {
	impl := (&turn.ToolTask{}).Impl()
	if impl.Migrate == nil {
		t.Fatal("pi.tool has no migration from version 1")
	}
	input, checkpoint := mustParse(t, `{"assistant":7,"callId":"c"}`), mustParse(t, `{"phase":"call"}`)
	gotInput, gotCheckpoint, err := impl.Migrate(input, checkpoint, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := jv.Stringify(gotInput); got != `{"kind":"model","assistant":7,"callId":"c"}` {
		t.Errorf("migrated input = %s", got)
	}
	if got := jv.Stringify(gotCheckpoint); got != `{"phase":"call"}` {
		t.Errorf("migrated checkpoint = %s", got)
	}
}
