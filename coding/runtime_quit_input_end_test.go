package coding

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// drainRecordingHost records the order of the two calls a dispose whose input ended makes on its extension host, and keeps the drain handler.
type drainRecordingHost struct {
	*recordingExtensionHost
	calls   []string
	handler func()
}

func (h *drainRecordingHost) SetRuntimeLoopDrainHandler(fn func()) {
	h.calls = append(h.calls, "drain handler")
	h.handler = fn
}

func (h *drainRecordingHost) EndInput() { h.calls = append(h.calls, "input ended") }

// A dispose whose input ended arms the drain handler, tells the extension host that input ended, and then emits session_shutdown as the plain dispose does. A drain report that arrives after it returned is not the mode's exit.
func TestInputEndedDisposeArmsTheDrainBeforeItTellsTheHostThatInputEnded(t *testing.T) {
	host := &drainRecordingHost{}
	exits := 0
	var order []string
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_shutdown": {func(...any) (any, error) {
				order = append(append(order, host.calls...), "session_shutdown")
				return nil, nil
			}},
		}}
	}})
	h.runtime.Session().extensionHost = host
	if h.runtime.emitQuitShutdownAfterInputEnd(func() { exits++ }) {
		t.Fatal("the dispose reports an exit though no runtime reported a drained loop")
	}
	if want := []string{"drain handler", "input ended", "session_shutdown"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("the dispose ran %v, want %v", order, want)
	}
	host.handler()
	if exits != 0 {
		t.Fatalf("a drain report after the dispose returned ran the exit %d times, want 0", exits)
	}
}

// A drain report while the dispose is pending is Pi's process ending under its pending dispose: the exit runs once, and the dispose reports that its caller must not go on.
func TestInputEndedDisposeEndsWithADrainReportWhileItIsPending(t *testing.T) {
	host := &drainRecordingHost{}
	exits := 0
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_shutdown": {func(...any) (any, error) { host.handler(); host.handler(); return nil, nil }},
		}}
	}})
	h.runtime.Session().extensionHost = host
	if !h.runtime.emitQuitShutdownAfterInputEnd(func() { exits++ }) {
		t.Fatal("the dispose returned to its caller though the exit had started")
	}
	if exits != 1 {
		t.Fatalf("the drain reports ran the exit %d times, want 1", exits)
	}
}

// Without an extension host the dispose is the plain one.
func TestInputEndedDisposeWithoutAnExtensionHostIsThePlainDispose(t *testing.T) {
	var reasons []string
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_shutdown": {func(args ...any) (any, error) {
				reasons = append(reasons, args[0].(extension.SessionShutdownEvent).Reason)
				return nil, nil
			}},
		}}
	}})
	h.runtime.EmitQuitShutdownAfterInputEnd(func() { t.Error("the exit ran without an extension host") })
	if err := h.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reasons, []string{"quit"}) {
		t.Fatalf("shutdown reasons = %v, want one quit", reasons)
	}
}

// loopDrainRecordingHost is a host that tells the two reports apart: handler takes a drained loop and a quit yield alike, loop a drained loop alone.
type loopDrainRecordingHost struct {
	*recordingExtensionHost
	handler func()
	loop    func()
}

func (h *loopDrainRecordingHost) SetRuntimeDrainHandler(fn func())     { h.handler = fn }
func (h *loopDrainRecordingHost) SetRuntimeLoopDrainHandler(fn func()) { h.loop = fn }
func (h *loopDrainRecordingHost) EndInput()                            {}

// A quit yield (runtime_quit_yield) is Pi's RPC exit at the stdout flush: a turn_end or agent_settled handler that suspended after the disposal. Pi's interactive quit
// has no such exit: its dispose ends in the drained loop or returns. So the quit's exit is installed as the handler of a drained loop alone, and a yield report, which
// the host gives only to the handler of both kinds, does not end the mode.
func TestInputEndedDisposeDoesNotEndWithAQuitYield(t *testing.T) {
	host := &loopDrainRecordingHost{}
	exits := 0
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_shutdown": {func(...any) (any, error) {
				// The yield report: the host runs the handler of both kinds, if one is installed.
				if host.handler != nil {
					host.handler()
				}
				return nil, nil
			}},
		}}
	}})
	h.runtime.Session().extensionHost = host
	if h.runtime.emitQuitShutdownAfterInputEnd(func() { exits++ }) || exits != 0 {
		t.Fatalf("a quit yield while the dispose was pending ran the exit %d times: the quit must end with a drained loop only", exits)
	}
	if host.loop == nil {
		t.Fatal("the quit installed no handler of a drained loop")
	}
}
