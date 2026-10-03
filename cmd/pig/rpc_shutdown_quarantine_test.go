//go:build unix

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// nodeRuntimeChildren counts the Node runtime processes the pig process at pid runs. Only Linux exposes the process tree without another tool; the children of every thread count, because Go starts a process from any thread.
func nodeRuntimeChildren(t *testing.T, pid int) int {
	t.Helper()
	tasks, err := filepath.Glob(filepath.Join("/proc", strconv.Itoa(pid), "task", "*", "children"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, task := range tasks {
		children, err := os.ReadFile(task)
		if err != nil {
			continue
		}
		for child := range strings.FieldsSeq(string(children)) {
			cmdline, err := os.ReadFile(filepath.Join("/proc", child, "cmdline"))
			if err == nil && strings.Contains(string(cmdline), "register-loader.mjs") {
				count++
			}
		}
	}
	return count
}

// runQuarantinedCommand loads rpc-shutdown.mjs, whose quit session_shutdown handler never settles, and rpc-shutdown-quarantine.mjs, sends the commands with the ids c, c1, c2, ... and ends stdin. Under pig the second extension crashed the shared Node process once, so the host runs it in a Node process of its own (node_recovery.go recoverNodeProcess, cell_plan.go PlanCells); Pi runs both in one process.
func runQuarantinedCommand(t *testing.T, start func(t *testing.T, extraEnv ...string) (*rpcProcess, string), pig bool, commands ...string) ([]rpcRecord, []string) {
	t.Helper()
	p, report := start(t, "RPC_SHUTDOWN_BLOCK_QUIT=1", rpcShutdownQuarantinedExtension)
	awaitReady(p)
	if pig {
		// The crash restarts the healthy members and the quarantined extension apart. The host announces neither on stdout.
		p.sendJSON(map[string]any{"id": "crash", "type": "prompt", "message": "/qcrash"})
		p.await("crash response", func(r rpcRecord) bool { return r["id"] == "crash" && r["type"] == "response" })
		waitForRPCShutdownReport(t, p, report, "qloaded", 2)
		waitForRPCActivated(t, p, report)
	}
	if pig && runtime.GOOS == "linux" {
		if got := nodeRuntimeChildren(t, p.cmd.Process.Pid); got != 2 {
			t.Fatalf("pig runs %d Node runtime processes, want the quit handler's and the quarantined extension's\n%s", got, p.stderr.String())
		}
	}
	for i, command := range commands {
		id := "c"
		if i > 0 {
			id += strconv.Itoa(i)
		}
		p.sendJSON(map[string]any{"id": id, "type": "prompt", "message": command})
	}
	p.closeInput()
	out := drainRPCOutput(p)
	status := waitForRPCTermination(t, p)
	if !status.Exited() || status.ExitStatus() != 0 {
		t.Fatalf("wait status = %v, want exit 0\n%s", status, p.stderr.String())
	}
	return out, readRPCShutdownReport(t, report)
}

// waitForRPCActivated sends /uiping and /qping until both extensions have recorded that their runtime is in RPC mode, which the host sets when it activates the runtime. A restarted Node process serves commands before the host activates it, and a command it serves earlier runs without its RPC invocation acknowledgment; the host lists the commands from before the crash and runs an unknown one as an ordinary prompt, so only the extensions' own records prove that each runtime is activated.
func waitForRPCActivated(t *testing.T, p *rpcProcess, report string) {
	t.Helper()
	deadline := time.Now().Add(p.budget)
	for i := 0; ; i++ {
		for _, command := range []string{"uiping", "qping"} {
			id := command + strconv.Itoa(i)
			p.sendJSON(map[string]any{"id": id, "type": "prompt", "message": "/" + command})
			p.await(command+" response", func(r rpcRecord) bool { return r["id"] == id && r["type"] == "response" })
		}
		entries := readRPCShutdownReport(t, report)
		if slices.Contains(entries, "uiping:rpc") && slices.Contains(entries, "qping:rpc") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the restarted runtimes were never activated: %v\n%s", entries, p.stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForRPCShutdownReport waits until the extension has recorded name at least count times. The host restarts a crashed Node process on its own schedule and announces nothing on stdout, so the report file is the only observation.
func waitForRPCShutdownReport(t *testing.T, p *rpcProcess, report, name string, count int) {
	t.Helper()
	deadline := time.Now().Add(p.budget)
	for {
		n := 0
		for _, entry := range readRPCShutdownReport(t, report) {
			if entry == name {
				n++
			}
		}
		if n >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the extension recorded %q %d times, want %d\n%s", name, n, count, p.stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func withoutReady(out []rpcRecord) []rpcRecord {
	kept := out[:0:0]
	for _, r := range out {
		if r["id"] != "ready" {
			kept = append(kept, r)
		}
	}
	return kept
}

// The Pi legs and the pig legs below run the same commands: only the process layout differs, and the observable output must not.
func TestRPCInputEndQuarantinedNodeCommandComparedWithPi(t *testing.T) {
	response := handledPromptResponse("c")
	for _, row := range []struct {
		name     string
		commands []string
		want     []rpcRecord
		report   []string
		why      string
	}{
		// Pi: a session change keeps the loop alive, so the response is written before the loop drains, in whichever extension the command runs. This is the N5 shape through crash-recovery quarantine.
		{"newSession", []string{"/qns"}, []rpcRecord{rpcShutdownStartedNotify, response}, nil, "the response of a command that awaits ctx.newSession() in another Node process"},
		// Pi: a pending dialog is not resolved by stdin EOF and does not keep the loop alive, so the process exits 0 after the request.
		{"dialog", []string{"/qask"}, []rpcRecord{{"type": "extension_ui_request", "method": "select", "title": "QAsk", "options": []any{"yes"}}}, nil, "only the select request"},
		// Pi: a Promise that nothing settles and that keeps no handle alive lets the loop drain: exit 0 with no response.
		{"never settles", []string{"/qhang"}, nil, nil, "no output"},
		// Pi: a child process keeps the loop alive until the command answers.
		{"child process", []string{"/qexec"}, []rpcRecord{response}, nil, "the response of a command that awaits a child process"},
		// Pi runs both commands in one loop, which the session change keeps alive, so the second response is written. Under pig the quarantined process can report its loop drained with only the first command before it reads the second; that report must not cover the second command.
		{"never settles, then newSession", []string{"/qhang", "/qns"}, []rpcRecord{rpcShutdownStartedNotify, handledPromptResponse("c1")}, nil, "the response of the second command, which awaits ctx.newSession()"},
		// Pi: neither command keeps the loop alive, so it drains and exits 0. Under pig the quarantined process must report its loop drained again for the command it read after its first report.
		{"never settles twice", []string{"/qhang", "/qhang"}, nil, nil, "no output"},
	} {
		t.Run(row.name, func(t *testing.T) {
			for _, impl := range rpcShutdownImplementations {
				t.Run(impl.name, func(t *testing.T) {
					out, _ := runQuarantinedCommand(t, impl.start, impl.name == "pig", row.commands...)
					out = withoutReady(out)
					if len(out) == 0 && len(row.want) == 0 {
						return
					}
					if !reflect.DeepEqual(out, row.want) {
						t.Fatalf("stdout = %v, want %s: %v", out, row.why, row.want)
					}
				})
			}
		})
	}
}
