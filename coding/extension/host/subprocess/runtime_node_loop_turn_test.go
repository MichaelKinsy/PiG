package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// loopTurnHoldSource handles session_shutdown by queueing an immediate and a zero-delay timer, each of which records whether the ctx is still live when it runs.
const loopTurnHoldSource = `import { appendFileSync } from "node:fs";
const LOG = %s;
const state = (ctx) => {
  try { ctx.cwd; return "live"; } catch (error) { return String(error?.message).includes("stale") ? "stale" : "threw " + error?.message; }
};
export default function (pi) {
  pi.on("session_shutdown", (_event, ctx) => {
    setImmediate(() => appendFileSync(LOG, "immediate " + state(ctx) + "\n"));
    setTimeout(() => appendFileSync(LOG, "timeout " + state(ctx) + "\n"), 0);
  });
}
`

// On Pi's one event loop the immediate an earlier session_shutdown handler queued runs before anything a later handler queues, so before that handler's continuation and the runner's invalidate. Probed with Pi 1.1.0 -p, a later handler awaiting setImmediate: "a immediate live", "b session_shutdown done", "a timeout stale". The host's invalidate can reach a waiting runtime together with the loop_turned before it; here a fake host writes both frames in one write, and the runtime still runs the queued immediate first, with a live ctx. The zero-delay timer runs by the clock: after the invalidate when the runtime reads both frames in one pass, and with a live ctx when it reads the turn alone and reaches its timers before it reads the invalidate (the residual in docs/extension-api-parity.md), so the test accepts either state for it.
func TestNodeLoopTurnRunsQueuedImmediatesBeforeLaterFrames(t *testing.T) {
	t.Parallel()
	peer, log := startLoopTurnShutdown(t, nil)
	// The runtime now waits for the host's next step. The turn and the invalidate after it arrive in one read.
	writeLoopTurnFrames(t, peer,
		Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyLoopTurned}},
		Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyInvalidate, Args: json.RawMessage(`{"message":"This extension ctx is stale after session replacement or reload."}`)}},
	)
	waitLoopTurnLog(t, log, "immediate live\ntimeout stale\n", "immediate live\ntimeout live\n")
}

// startLoopTurnShutdown starts a print-mode runtime for loopTurnHoldSource on a fake host, sends it the run_signal frames before, then its session_shutdown request, and reads up to the response, after which the runtime waits for the host's next step.
func startLoopTurnShutdown(t *testing.T, before []Envelope) (net.Conn, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	parent := "/tmp"
	if runtime.GOOS == "windows" {
		parent = os.TempDir()
	}
	root, err := os.MkdirTemp(parent, "pig-loop-turn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	log := filepath.Join(root, "loop-turn.log")
	entry := filepath.Join(root, "loop-turn.mjs")
	if err := os.WriteFile(entry, []byte(strings.Replace(loopTurnHoldSource, "%s", strconv.Quote(log), 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, address, err := ListenExtension(filepath.Join(root, "runtime.sock"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	cmd := exec.CommandContext(t.Context(), node, filepath.Join("runtime-node", "cli.mjs"), entry)
	cmd.Env = append(os.Environ(), "PIG_EXT_SOCKET="+address, "PIG_EXT_NAME=loop-turn")
	peer := startAndAccept(t, cmd, listener)
	t.Cleanup(func() { _ = peer.Close() })
	register := readLivenessEnvelope(t, peer)
	if register.Type != MsgRegister || register.Register == nil || len(register.Register.Handlers) != 1 {
		t.Fatalf("register = %+v", register)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgReady, Ready: &ReadyPayload{Cwd: root, Mode: "print", Width: 80}})
	for _, env := range before {
		writeLivenessEnvelope(t, peer, env)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: "shutdown", Request: &RequestPayload{Method: "event", Event: "session_shutdown", HandlerID: register.Register.Handlers[0].HandlerID, Args: json.RawMessage(`{"type":"session_shutdown","reason":"quit"}`)}})
	for {
		if env := readLivenessEnvelope(t, peer); env.Type == MsgResponse && env.ID == "shutdown" {
			return peer, log
		}
	}
}

// writeLoopTurnFrames writes frames to the runtime in one write, so they arrive in one read.
func writeLoopTurnFrames(t *testing.T, peer net.Conn, envs ...Envelope) {
	t.Helper()
	var frames []byte
	for _, env := range envs {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		frames = binary.BigEndian.AppendUint32(frames, uint32(len(body)))
		frames = append(frames, body...)
	}
	if _, err := peer.Write(frames); err != nil {
		t.Fatal(err)
	}
}

// waitLoopTurnLog waits until the log holds as many lines as want and compares it with want and each of also.
func waitLoopTurnLog(t *testing.T, log, want string, also ...string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		data, _ := os.ReadFile(log)
		if strings.Count(string(data), "\n") >= strings.Count(want, "\n") {
			if string(data) != want && !slices.Contains(also, string(data)) {
				t.Fatalf("log = %q, want %q", data, append([]string{want}, also...))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log = %q after 20s, want %q", data, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A run beginning is the host's next step, but the host sends a run's run_signal frames to each connection, and a packed process reads its sockets in no fixed order: a member with no request during the run reads the run's frames before its first request, which can come after another member answered session_shutdown and began the wait. Pig 75-print-shutdown-loop-turn printed "a immediate live" before "b session_shutdown" in a quarter of runs. A run the process has already read of is no run beginning: a frame of it leaves the wait, and the callbacks the handler queued, for the host's real next step, here the invalidate.
func TestNodePromptEndWaitIgnoresAFinishedRunsSignal(t *testing.T) {
	t.Parallel()
	run := func(active bool) Envelope {
		return Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "run_signal", Args: json.RawMessage(fmt.Sprintf(`{"run":1,"active":%t,"aborted":false}`, active))}}
	}
	peer, log := startLoopTurnShutdown(t, []Envelope{run(true), run(false)})
	writeLoopTurnFrames(t, peer, run(true), run(false))
	// The runtime answers this synchronous request while it waits, and from its event loop after the wait: once the answer is read, a wait the replay ended has run the queued immediate.
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: "probe", Request: &RequestPayload{Method: "autocomplete.sync"}})
	for {
		if env := readLivenessEnvelope(t, peer); env.Type == MsgResponse && env.ID == "probe" {
			break
		}
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyInvalidate, Args: json.RawMessage(`{"message":"This extension ctx is stale after session replacement or reload."}`)}})
	waitLoopTurnLog(t, log, "immediate stale\ntimeout stale\n")
}
