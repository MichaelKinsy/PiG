package turn_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// Pi v1.6.0 bumped pi.generation to v3 (afterTools, yield) and pi.tool to v4 (parked). This core accepts v3/v4 records
// for store/handoff compatibility, and must explicitly reject stored checkpoints in those unimplemented suspendable-work
// phases rather than mis-executing them.
func TestSuspendablePhasesAreExplicitlyRejected(t *testing.T) {
	rt := &session.Runtime{}
	gen := (&turn.Generation{}).Impl()
	for _, phase := range []string{"afterTools", "yield"} {
		task := jv.NewObject()
		state := jv.NewObject()
		cp := jv.NewObject()
		cp.Set("phase", phase)
		state.Set("checkpoint", cp)
		task.Set("state", state)
		var got error
		gen.Phase(rt, task, func(err error) { got = err })
		if got == nil || !strings.Contains(got.Error(), "suspendable work") || !strings.Contains(got.Error(), phase) {
			t.Fatalf("generation phase %q err = %v, want explicit suspendable work rejection", phase, got)
		}
	}

	tool := (&turn.ToolTask{}).Impl()
	task := jv.NewObject()
	state := jv.NewObject()
	cp := jv.NewObject()
	cp.Set("phase", "parked")
	state.Set("checkpoint", cp)
	task.Set("state", state)
	var got error
	tool.Phase(rt, task, func(err error) { got = err })
	if got == nil || !strings.Contains(got.Error(), "suspendable work") || !strings.Contains(got.Error(), "parked") {
		t.Fatalf("tool phase parked err = %v, want explicit suspendable work rejection", got)
	}
}
