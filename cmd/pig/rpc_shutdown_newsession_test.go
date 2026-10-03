//go:build unix

package main

import (
	"reflect"
	"testing"
)

// Pi 0.87.1: a command that awaits ctx.newSession() and then returns answers after stdin ended, while a quit session_shutdown handler that never settles keeps the dispose pending. Pi's replacement build is in-process work, so the event loop stays alive until the command resolves and its response is written; then the loop drains and the process exits 0. Pig must not count the command as suspended while the host's newSession call builds the replacement.
func TestRPCInputEndAfterCommandNewSessionAnswersComparedWithPi(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, _ := impl.start(t, "RPC_SHUTDOWN_BLOCK_QUIT=1")
			p.send(`{"id":"c","type":"prompt","message":"/ns"}`)
			p.closeInput()
			out := drainRPCOutput(p)
			for _, r := range out {
				if r["type"] == "extension_ui_request" {
					delete(r, "id")
				}
			}
			status := waitForRPCTermination(t, p)
			if !status.Exited() || status.ExitStatus() != 0 {
				t.Fatalf("wait status = %v, want exit 0", status)
			}
			want := []rpcRecord{
				{"type": "extension_ui_request", "method": "notify", "message": "session_shutdown started", "notifyType": "info"},
				handledPromptResponse("c"),
			}
			if !reflect.DeepEqual(out, want) {
				t.Fatalf("stdout = %v, want the replacement notify and the command response", out)
			}
		})
	}
}

// Pi 0.87.1 runs every extension in one process, so its loop drains only after every command has written its response. Pig packs two Node extensions into one process with one connection each: ns2 belongs to the second extension, and the never-settling quit handler of the first reports the drain. The drain exit must wait for the response on the second connection too.
func TestRPCInputEndAfterSecondExtensionCommandNewSessionAnswersComparedWithPi(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, _ := impl.start(t, "RPC_SHUTDOWN_BLOCK_QUIT=1", rpcShutdownSecondExtension)
			p.send(`{"id":"c","type":"prompt","message":"/ns2"}`)
			p.closeInput()
			out := drainRPCOutput(p)
			status := waitForRPCTermination(t, p)
			if !status.Exited() || status.ExitStatus() != 0 {
				t.Fatalf("wait status = %v, want exit 0", status)
			}
			want := []rpcRecord{
				{"type": "extension_ui_request", "method": "notify", "message": "session_shutdown started", "notifyType": "info"},
				handledPromptResponse("c"),
			}
			if !reflect.DeepEqual(out, want) {
				t.Fatalf("stdout = %v, want the replacement notify and the command response", out)
			}
		})
	}
}
