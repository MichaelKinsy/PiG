package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// Pi 1.5.0 shutdownHandler (rpc-mode.ts) only set a flag that a stdin command or agent_settled checked, so a ctx.shutdown() from a timer on an idle session waited for the next command (Pi #11599, fixed on Pi main by 1708b4788, D231). The process must shut down on its own.
//
// The request must arrive while the session is idle with input attached, so the extension makes it only after the test saw the get_state answer (RPC_SHUTDOWN_IDLE_GATE). A timer armed at session_start races that answer: when startup after session_start takes longer than the timer, the request is made before input is attached, the notification is written ahead of the get_state response, and awaitReady discards it. A 300 ms startup tail stands in for a slow runner, in both modes.
func TestRPCExtensionShutdownWhileIdleExitsWithoutAnotherCommand(t *testing.T) {
	// One extension runs in its own process; two share a packed runtime cell (D20), which must behave alike.
	for name, extra := range map[string][]string{"isolated": nil, "packed": {rpcShutdownSecondExtension}} {
		t.Run(name, func(t *testing.T) {
			gate := filepath.Join(t.TempDir(), "idle-gate")
			p, report := startRPCShutdownFixture(t, append([]string{"RPC_SHUTDOWN_IDLE_GATE=" + gate, "RPC_SHUTDOWN_START_TAIL=300"}, extra...)...)
			awaitReady(p)
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			records := drainRPCOutput(p)
			p.waitForExit("after an idle ctx.shutdown() with stdin open")
			if !reflect.DeepEqual(records, []rpcRecord{rpcShutdownStartedNotify}) {
				t.Fatalf("stdout = %v, want the session_shutdown notification", records)
			}
			if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"session_shutdown"}) {
				t.Fatalf("extension events = %v, want [session_shutdown]", events)
			}
		})
	}
}

// A request made before input is attached (a synchronous ctx.shutdown() in session_start) is checked once input is attached.
func TestRPCExtensionShutdownFromSessionStartExits(t *testing.T) {
	p, report := startRPCShutdownFixture(t, "RPC_SHUTDOWN_AT_START=1")
	records := drainRPCOutput(p)
	p.waitForExit("after ctx.shutdown() in session_start")
	if !reflect.DeepEqual(records, []rpcRecord{rpcShutdownStartedNotify}) {
		t.Fatalf("stdout = %v, want the session_shutdown notification", records)
	}
	if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"session_shutdown"}) {
		t.Fatalf("extension events = %v, want [session_shutdown]", events)
	}
}

// A shutdown requested by an extension command that keeps running waits until the command finishes, and the prompt response is written first.
func TestRPCExtensionCommandShutdownWritesResponseBeforeShutdown(t *testing.T) {
	p, report := startRPCShutdownFixture(t)
	awaitReady(p)
	p.sendJSON(map[string]any{"id": "q", "type": "prompt", "message": "/quitslow"})
	records := drainRPCOutput(p)
	p.waitForExit("after a command requested shutdown")
	if want := []rpcRecord{handledPromptResponse("q"), rpcShutdownStartedNotify}; !reflect.DeepEqual(records, want) {
		t.Fatalf("stdout = %v, want %v", records, want)
	}
	if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"quitslow", "quitslow:done", "session_shutdown"}) {
		t.Fatalf("extension events = %v, want shutdown after the command finished", events)
	}
}

// A command that starts while a request is pending runs to its response before the shutdown: here /quit requests it and /slow, read in the same chunk, is already in flight when /quit finishes.
func TestRPCExtensionShutdownWaitsForCommandOfSameChunk(t *testing.T) {
	p, report := startRPCShutdownFixture(t)
	awaitReady(p)
	p.send(`{"id":"q","type":"prompt","message":"/quit"}` + "\n" + `{"id":"s","type":"prompt","message":"/slow"}`)
	records := drainRPCOutput(p)
	p.waitForExit("after /quit and /slow")
	if want := []rpcRecord{handledPromptResponse("q"), handledPromptResponse("s"), rpcShutdownStartedNotify}; !reflect.DeepEqual(records, want) {
		t.Fatalf("stdout = %v, want %v", records, want)
	}
	if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"quit", "slow", "slow:done", "session_shutdown"}) {
		t.Fatalf("extension events = %v", events)
	}
}
