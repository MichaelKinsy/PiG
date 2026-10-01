//go:build unix

package main

import (
	"reflect"
	"syscall"
	"testing"
)

// Pi 0.87.1: a command that replaces the Session, stdin ending at once and a quit session_shutdown handler that never settles while it keeps the event loop alive: Pi answers the command, prints no extension_error, and stays alive until a signal ends it. Pig retires the replaced Session's host when the command returns; it must not shut that host down under the pending quit emission. The held loop makes the ordering deterministic: neither process can exit before the command answers.
func TestRPCInputEndAfterCommandReplacementKeepsPendingQuitShutdownQuiet(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, _ := impl.start(t, "RPC_SHUTDOWN_HOLD_QUIT=1")
			p.send(`{"id":"c","type":"prompt","message":"/ns"}`)
			p.closeInput()
			var out []rpcRecord
			p.await("the command response", func(r rpcRecord) bool {
				if r["type"] == "extension_ui_request" {
					delete(r, "id")
				}
				out = append(out, r)
				return isSuccessResponse(r, "c")
			})
			if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			out = append(out, drainRPCOutput(p)...)
			status := waitForRPCTermination(t, p)
			if !status.Signaled() || status.Signal() != syscall.SIGTERM {
				t.Fatalf("wait status = %v, want SIGTERM", status)
			}
			want := []rpcRecord{
				{"type": "extension_ui_request", "method": "notify", "message": "session_shutdown started", "notifyType": "info"},
				{"type": "response", "id": "c", "command": "prompt", "success": true},
			}
			if !reflect.DeepEqual(out, want) {
				t.Fatalf("stdout = %v, want the replacement notify and the command response only", out)
			}
		})
	}
}
