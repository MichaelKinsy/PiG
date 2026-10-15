package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestAgentSettledDefersRunStartingActionsUntilAllHandlersReturn(t *testing.T) {
	var lifecycle []string
	mode := &InteractiveMode{}
	ext := extension.Extension{Path: "settled", Handlers: map[string][]extension.HandlerFn{
		EventAgentSettled: {
			func(...any) (any, error) {
				lifecycle = append(lifecycle, "first")
				start := func() { lifecycle = append(lifecycle, "start") }
				if !mode.deferSettledAction(start) {
					start()
				}
				return nil, nil
			},
			func(...any) (any, error) {
				lifecycle = append(lifecycle, "second")
				return nil, nil
			},
		},
	}}
	mode.newRunner = inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	mode.emitAgentSettledEvent()
	if want := []string{"first", "second", "start"}; !reflect.DeepEqual(lifecycle, want) {
		t.Fatalf("lifecycle = %v, want %v", lifecycle, want)
	}
}

// Pi 1.1.0 agent-session.ts _emitAgentSettled passes `aborted` to the extension runner (#10607): interactive mode, which drives its own settlement, reports it too.
func TestAgentSettledReportsAbortedToExtensions(t *testing.T) {
	var got []bool
	ext := extension.Extension{Path: "settled", Handlers: map[string][]extension.HandlerFn{
		EventAgentSettled: {func(args ...any) (any, error) {
			got = append(got, args[0].(extension.AgentSettledEvent).Aborted)
			return nil, nil
		}},
	}}
	mode := &InteractiveMode{}
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	mode.emitAgentSettledFor(runner, true)
	mode.emitAgentSettledFor(runner, false)
	if want := []bool{true, false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("aborted = %v, want %v", got, want)
	}
}
