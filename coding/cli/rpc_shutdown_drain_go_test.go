//go:build unix

package cli

import (
	"path/filepath"
	"reflect"
	"testing"
)

// startRPCShutdownGoFixture starts RPC mode with rpc-shutdown.mjs, a quit session_shutdown handler that never settles, and the Go SDK extension testdata/rpc-shutdown-go.
func startRPCShutdownGoFixture(t *testing.T) *rpcProcess {
	t.Helper()
	return startRPCShutdownGoFixtureWith(t)
}

// startRPCShutdownGoFixtureWith is startRPCShutdownGoFixture with extraEnv added to the process environment.
func startRPCShutdownGoFixtureWith(t *testing.T, extraEnv ...string) *rpcProcess {
	t.Helper()
	goExt := buildGoSDKFixture(t, "rpc-shutdown-go")
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	report := filepath.Join(t.TempDir(), "report.txt")
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "RPC_SHUTDOWN_REPORT=" + report, "RPC_SHUTDOWN_BLOCK_QUIT=1"}
	env = append(env, extraEnv...)
	return startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1", "-e", fixture, "-e", goExt)
}

// Pi 0.87.1: a pending dialog is not resolved by stdin EOF, and a quit session_shutdown handler that never settles leaves the process to exit when the event loop drains (rpc-mode.ts:728-744). A dialog does not keep that loop alive, so the process exits 0 after the select request and prints no response, while a command that waits on a child process does keep it alive. A Go SDK command awaiting a select is a suspension boundary for the host, yet nothing counts it suspended until the drain checkpoint.
func TestRPCInputEndDrainExitCountsGoSDKCommandAwaitingDialogSuspended(t *testing.T) {
	p := startRPCShutdownGoFixture(t)
	p.send(`{"id":"c","type":"prompt","message":"/goask"}`)
	p.closeInput()
	out := drainRPCOutput(p)
	status := waitForRPCTermination(t, p)
	if !status.Exited() || status.ExitStatus() != 0 {
		t.Fatalf("wait status = %v, want exit 0", status)
	}
	want := []rpcRecord{{"type": "extension_ui_request", "method": "select", "title": "GoAsk", "options": []any{"yes"}}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("stdout = %v, want only the select request", out)
	}
}

// Pi 0.87.1: a command awaiting pi.exec keeps the event loop alive through its child process, so the response is written before the loop drains, although a quit session_shutdown handler never settles. The Pi leg runs the TypeScript /exec row; the pig leg runs a Go SDK command that awaits ctx.Exec, which the drain checkpoint must not count suspended.
func TestRPCInputEndDrainExitKeepsGoSDKCommandAwaitingExecComparedWithPi(t *testing.T) {
	for _, leg := range []struct {
		name    string
		start   func(t *testing.T) *rpcProcess
		command string
	}{
		{"pig", startRPCShutdownGoFixture, "/goexec"},
		{"pi", func(t *testing.T) *rpcProcess {
			p, _ := startPiRPCShutdownFixture(t, "RPC_SHUTDOWN_BLOCK_QUIT=1")
			return p
		}, "/exec"},
	} {
		t.Run(leg.name, func(t *testing.T) {
			p := leg.start(t)
			p.send(`{"id":"c","type":"prompt","message":"` + leg.command + `"}`)
			p.closeInput()
			out := drainRPCOutput(p)
			status := waitForRPCTermination(t, p)
			if !status.Exited() || status.ExitStatus() != 0 {
				t.Fatalf("wait status = %v, want exit 0", status)
			}
			want := []rpcRecord{handledPromptResponse("c")}
			if !reflect.DeepEqual(out, want) {
				t.Fatalf("stdout = %v, want the command response", out)
			}
		})
	}
}

// A runtime with no microtask continuation reports no window, so its settle-tail handler holds stdin while it runs. Once input ended the host counts it as waiting, as FlushCommands counts such a runtime's commands, so stdin's end starts shutdown rather than waiting forever for a handler Pi has no counterpart of. The handler waits until the shutdown cancels it.
func TestRPCInputEndDuringGoSDKSettleTailHandlerShutsDown(t *testing.T) {
	p := startRPCShutdownGoFixtureWith(t, "RPC_SHUTDOWN_GO_SETTLED_BLOCK=1")
	awaitReady(p)
	p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
	p.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
	p.closeInput()
	drainRPCOutput(p)
	if status := waitForRPCTermination(t, p); !status.Exited() || status.ExitStatus() != 0 {
		t.Fatalf("wait status = %v, want exit 0\n%s", status, p.stderr.String())
	}
}
