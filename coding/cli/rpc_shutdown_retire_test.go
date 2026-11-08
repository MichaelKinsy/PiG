//go:build unix

package cli

import (
	"reflect"
	"syscall"
	"testing"
)

// Pi (pin c23b9ec1): a command that replaces the Session, stdin ending at once and a quit session_shutdown handler that never settles while it keeps the event loop alive: Pi answers the command, prints no extension_error, and stays alive until a repeated signal ends it with 143: the first signal does not interrupt the started shutdown (rpc-mode.ts:383-391). Pig retires the replaced Session's host when the command returns; it must not shut that host down under the pending quit emission. The held loop makes the ordering deterministic: neither process can exit before the command answers.
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
			signalRPC(t, p, syscall.SIGTERM, syscall.SIGTERM)
			out = append(out, drainRPCOutput(p)...)
			requireExited(t, waitForRPCTermination(t, p), 143)
			want := []rpcRecord{
				{"type": "extension_ui_request", "method": "notify", "message": "session_shutdown started", "notifyType": "info"},
				handledPromptResponse("c"),
			}
			if !reflect.DeepEqual(out, want) {
				t.Fatalf("stdout = %v, want the replacement notify and the command response only", out)
			}
		})
	}
}
