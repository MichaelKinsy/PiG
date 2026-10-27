package subprocess

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// runBeginningSource queues an immediate in its input handler and records the order of the handlers and the immediate.
const runBeginningSource = `import { appendFileSync } from "node:fs";
const LOG = %s;
export default function (pi) {
  pi.on("input", () => {
    appendFileSync(LOG, "input\n");
    setImmediate(() => appendFileSync(LOG, "input-immediate\n"));
  });
  pi.on("session_shutdown", () => appendFileSync(LOG, "session_shutdown\n"));
}
`

// Pig 74-print-input-run: Pi's run makes a loop turn at the bash tool's I/O, so the immediate a print prompt's input handler queued runs during the run, before session_shutdown. The host sends the run beginning as the step that ends the runtime's wait after the input, and a runtime the OS scheduled late reads the run's end and the shutdown request in the same pass (a macOS runner running eight copies of the scenario lost the immediate in half of the runs). A fake host writes the three frames in one write, so they arrive in one read: the shutdown request must still wait for the immediate.
func TestNodeRunBeginningRunsTheInputsImmediateBeforeTheFramesReadWithIt(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	parent := "/tmp"
	if runtime.GOOS == "windows" {
		parent = os.TempDir()
	}
	root, err := os.MkdirTemp(parent, "pig-run-beginning-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	log := filepath.Join(root, "run-beginning.log")
	entry := filepath.Join(root, "run-beginning.mjs")
	if err := os.WriteFile(entry, []byte(strings.Replace(runBeginningSource, "%s", strconv.Quote(log), 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, address, err := ListenExtension(filepath.Join(root, "runtime.sock"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	cmd := exec.CommandContext(t.Context(), node, filepath.Join("runtime-node", "cli.mjs"), entry)
	cmd.Env = append(os.Environ(), "PIG_EXT_SOCKET="+address, "PIG_EXT_NAME=run-beginning")
	peer := startAndAccept(t, cmd, listener)
	t.Cleanup(func() { _ = peer.Close() })
	register := readLivenessEnvelope(t, peer)
	if register.Type != MsgRegister || register.Register == nil {
		t.Fatalf("register = %+v", register)
	}
	handlers := map[string]int{}
	for _, handler := range register.Register.Handlers {
		handlers[handler.Event] = handler.HandlerID
	}
	if handlers["input"] == 0 || handlers["session_shutdown"] == 0 {
		t.Fatalf("handlers = %v", handlers)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgReady, Ready: &ReadyPayload{Cwd: root, Mode: "print", Width: 80}})
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: "input", Request: &RequestPayload{Method: "event", Event: "input", HandlerID: handlers["input"], Args: json.RawMessage(`{"type":"input","text":"Run: expr 20 + 22","source":"interactive"}`)}})
	for {
		if env := readLivenessEnvelope(t, peer); env.Type == MsgResponse && env.ID == "input" {
			break
		}
	}
	// The runtime now waits for the host's next step: the run beginning, the run's end and the shutdown request arrive in one read.
	run := func(active bool) Envelope {
		args := `{"run":1,"active":false,"aborted":false}`
		if active {
			args = `{"run":1,"active":true,"aborted":false}`
		}
		return Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "run_signal", Args: json.RawMessage(args)}}
	}
	writeLoopTurnFrames(t, peer, run(true), run(false),
		Envelope{Type: MsgRequest, ID: "shutdown", Request: &RequestPayload{Method: "event", Event: "session_shutdown", HandlerID: handlers["session_shutdown"], Args: json.RawMessage(`{"type":"session_shutdown","reason":"quit"}`)}},
	)
	waitLoopTurnLog(t, log, "input\ninput-immediate\nsession_shutdown\n")
}
