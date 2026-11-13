package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Node extension whose handler holds its thread cannot acknowledge a reload's invalidation, and the reload must still replace it. Pi is never in this state: its extensions run on the thread that runs reload(), so the reload does not run while a handler holds that thread. Here the reload runs in another process: the Go fixture's /rl awaits ctx.reload(), and its session_shutdown handler keeps the reload before its invalidation point until the Node extension's /hang, which was awaiting, has blocked its own thread. (RPC mode reads no input behind a handler that runs without awaiting, as Pi's loop reads none, so the reload is under way before the thread is blocked.) The host waits for the acknowledgement for the pong deadline, then ends the Node extension: the prompt of the command in flight on it answers, the reload loads the extension again and answers, and stderr, where this mode writes the host's lifecycle notices, says so in one line. No crash notice stands beside it.
// mutation-checked: with the ReloadDeadline branch of handleIncoming off stderr also has `extension "hang" disabled: extension_unresponsive: ...`; without the lines of ReloadReport.Unacknowledged in reloadBuildExtensions stderr has no line.
func TestRPCReloadReplacesANodeExtensionThatDoesNotAcknowledgeIt(t *testing.T) {
	hang := filepath.Join(t.TempDir(), "hang.mjs")
	// The handler awaits its gate and then blocks its thread for good.
	code := `import { existsSync, writeFileSync } from "node:fs";
const dir = process.env.RL_DIR;
export default function (pi) {
  pi.registerCommand("hang", { description: "hang", handler: async (_args, ctx) => {
    ctx.ui.notify("hang running", "info");
    while (!existsSync(dir + "/hang-gate")) await new Promise((resolve) => setTimeout(resolve, 2));
    writeFileSync(dir + "/hang-blocked", "");
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0);
  } });
}
`
	if err := os.WriteFile(hang, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	f := startReloadRPCWith(t, []string{"RL_SHUTDOWN=await"}, reloadFixture(t, "go"), hang)
	f.p.send(f.prompt("c", "/hang"))
	f.expectStream("the hang command", "notify hang running")
	f.p.send(f.prompt("b", "/rl"))
	f.expectStream("the reload's session_shutdown handler", "notify shutting down g1")
	f.open("hang-gate")
	f.reached("hang-blocked")
	f.open("shutdown-gate")
	f.expectStream("the reload of an extension that does not acknowledge it", "notify shutdown done g1", "response c success=true", "notify started g2", "response b success=true")
	f.finish()
	var lines, notices []string
	for line := range strings.SplitSeq(f.p.stderr.String(), "\n") {
		switch {
		case strings.Contains(line, "did not acknowledge the reload"):
			lines = append(lines, line)
		case strings.Contains(line, "crashed") || strings.Contains(line, "disabled") || strings.Contains(line, "extension_unresponsive"):
			notices = append(notices, line)
		}
	}
	if want := `extension "hang" did not acknowledge the reload within 5s and was restarted`; len(lines) != 1 || lines[0] != want || len(notices) != 0 {
		t.Fatalf("stderr says %q and %q, want the one line %q\n%s", lines, notices, want, f.p.stderr.String())
	}
}
