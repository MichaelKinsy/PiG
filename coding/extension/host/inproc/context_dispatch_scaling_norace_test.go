//go:build !race

package inproc_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// A `context` dispatch runs once per provider request, so what it costs beyond the handlers' own work must not grow with the history. Subprocess handlers that leave the conversation as it was answer with the unchanged report alone (no list), so the runner neither copies the conversation for them (they cannot edit its Go values) nor decodes an echoed list: allocations of the dispatch do not depend on the number of messages. Before, the runner deep-cloned every message and each handler's echoed list was decoded again.
func TestScalingContextDispatchOfUnchangedSubprocessHandlers(t *testing.T) {
	for _, event := range []string{"context", "context_with_system"} {
		testscaling.Check(t, testscaling.Case{Name: event + " dispatch of four unchanged subprocess handlers", Size: 100, Bound: testscaling.Constant, Prepare: func(n int) func() {
			runner := inproc.NewRunner([]extension.Extension{jsonContextExtension(event, 4, `{"_pigContextUnchanged":true}`)}, "")
			messages := contextHistory(n)
			return func() {
				var err error
				if event == "context" {
					_, _, err = runner.EmitContextTracked(t.Context(), messages)
				} else {
					_, err = runner.EmitContextWithSystem(t.Context(), messages)
				}
				if err != nil {
					panic(err)
				}
			}
		}})
	}
}
