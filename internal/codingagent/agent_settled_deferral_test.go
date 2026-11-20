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

// A quit that is in progress still starts the run of a deferred settled action once every handler returned: the pinned Pi, quit under tmux while an agent_settled handler sends a user
// message, runs the turn of that message and emits session_shutdown after it settled (5 of 5 runs for each of ctrl+d, ctrl+c twice and /quit; scenario 115-quit-settled-send-user-message-*).
func TestAgentSettledStartsDeferredStartsOnceExitIsRequested(t *testing.T) {
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
	mode.requestExit.Store(true)
	mode.emitAgentSettledEvent()
	if want := []string{"first", "second", "start"}; !reflect.DeepEqual(lifecycle, want) {
		t.Fatalf("lifecycle = %v, want %v", lifecycle, want)
	}
	if len(mode.deferredSettledActions) != 0 {
		t.Fatalf("%d deferred actions stay queued", len(mode.deferredSettledActions))
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
