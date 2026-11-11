package session_test

import (
	"errors"
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// An entry whose message strict JSON cannot carry fails the commit with Chord's copyJson text
// (packages/chord/src/json.ts: "Value contains a non-finite number and is not strict JSON" and "Value contains a
// non-JSON function; expected strict JSON"), which a faulted run reports as its submission's detail, not with
// encoding/json's own text.
func TestAnEntryWhoseMessageIsNotStrictJSONFailsWithChordsText(t *testing.T) {
	nonFinite := ai.AssistantMessage{API: "faux", Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop}
	nonFinite.Usage.Cost.Total = math.NaN()
	function := ai.AssistantMessage{API: "faux", Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop}
	function.Diagnostics = []ai.AssistantMessageDiagnostic{{Type: "probe", Details: map[string]any{"invalid": func() {}}}}
	for name, tc := range map[string]struct {
		message ai.AssistantMessage
		text    string
	}{
		"a non-finite number": {nonFinite, "Value contains a non-finite number and is not strict JSON"},
		"a function":          {function, "Value contains a non-JSON function; expected strict JSON"},
	} {
		g := openGC(t, 1)
		_, err := g.session.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(g.ids[0], durable.EntryDraft{Kind: "message", Model: []ai.Message{tc.message}})
		})
		if !errors.Is(err, delta.ErrNotStrictJSON) || err.Error() != tc.text {
			t.Errorf("%s: err = %v, want %q", name, err, tc.text)
		}
		g.close()
	}
}
