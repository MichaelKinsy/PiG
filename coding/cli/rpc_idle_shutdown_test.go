package cli

import (
	"reflect"
	"slices"
	"testing"
)

// Pi 1.5.0 shutdownHandler (rpc-mode.ts) only set a flag that a stdin command or agent_settled checked, so a ctx.shutdown() from a timer on an idle session waited for the next command (Pi #11599, fixed on Pi main by 1708b4788, D231). The process must shut down on its own.
func TestRPCExtensionShutdownWhileIdleExitsWithoutAnotherCommand(t *testing.T) {
	p, report := startRPCShutdownFixture(t, "RPC_SHUTDOWN_IDLE_TIMER=100")
	awaitReady(p)
	records := drainRPCOutput(p)
	p.waitForExit("after an idle ctx.shutdown() with stdin open")
	if !reflect.DeepEqual(records, []rpcRecord{rpcShutdownStartedNotify}) {
		t.Fatalf("stdout = %v, want the session_shutdown notification", records)
	}
	if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"session_shutdown"}) {
		t.Fatalf("extension events = %v, want [session_shutdown]", events)
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
