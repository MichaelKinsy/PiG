package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// eventHandlerPayloadProbe registers two handlers of one event of one extension: the first writes a sentinel into a message object nested in the event, the second
// logs what it reads from the same place.
const eventHandlerPayloadProbe = `import { appendFileSync } from "node:fs";
export default function (pi) {
  pi.on("turn_end", async (event) => {
    event.context.contextMessages[0].sentinel = "WRITTEN-BY-THE-FIRST-HANDLER";
  });
  pi.on("turn_end", async (event) => {
    appendFileSync(process.env.PAYLOAD_PROBE_LOG, "second:" + String(event.context.contextMessages[0].sentinel) + "\n");
  });
}
`

// D657, both sides. One extension registers two handlers of the turn_end event; the first writes a property into the first message of event.context.contextMessages
// and the second reads it. The pinned Pi builds the context of a boundary event over the session's live message objects (runner.ts emitBoundary, a new event object
// for each handler whose context holds those objects), so the second handler reads the first's write. PiG decodes a copy of the payload for each handler, so its
// second handler reads none. (A context event is not the probe: PiG's session carries an in-place edit of a message from one context handler to the next, as the
// handler's return value would, so its messages are shared as Pi's are.) The Pi row fails the day Pi changes its side: that is when D657's remove-when is checked.
func TestEventHandlersOfOneDispatchShareTheirPayloadInPiAndNotInPiG(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and runs the pinned Pi and a Node extension")
	}
	for _, impl := range []struct {
		name  string
		want  string
		start func(t *testing.T, logPath, entry string) *rpcProcess
	}{
		{"pi", "second:WRITTEN-BY-THE-FIRST-HANDLER", func(t *testing.T, logPath, entry string) *rpcProcess {
			return startPiRPCFaux(t, []string{"PAYLOAD_PROBE_LOG=" + logPath}, "-e", entry)
		}},
		{"pig", "second:undefined", func(t *testing.T, logPath, entry string) *rpcProcess {
			home := t.TempDir()
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_OFFLINE=1", "PAYLOAD_PROBE_LOG=" + logPath}
			return startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1", "-e", entry)
		}},
	} {
		t.Run(impl.name, func(t *testing.T) {
			entry := filepath.Join(t.TempDir(), "payload-probe.mjs")
			writeTestFile(t, entry, eventHandlerPayloadProbe)
			logPath := filepath.Join(t.TempDir(), "payload.log")
			process := impl.start(t, logPath, entry)
			process.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
			process.await("the prompt response", func(r rpcRecord) bool { return isSuccessResponse(r, "p") })
			awaitReloadLogLine(t, logPath, "second:")
			process.closeAndWait("event payload probe")
			if got := strings.Join(readReloadLog(t, logPath), "\n"); got != impl.want {
				t.Errorf("second handler log:\n%s\nwant:\n%s", got, impl.want)
			}
		})
	}
}
