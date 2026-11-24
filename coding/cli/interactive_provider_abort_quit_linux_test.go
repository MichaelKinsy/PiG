//go:build linux

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// quitProbeNode registers an extension provider whose stream shows "alpha beta " and then waits for the abort of its request. When the signal aborts it writes the file
// @SEEN@, and then: author-error pushes an error event that holds its partial and ends; author-ignores goes on for 0.3 s on a timer and completes; author-never does nothing
// more: it neither pushes nor ends its stream, and it holds no timer and no I/O; author-held does not go on with its stream either, but holds a timer.
const quitProbeNode = `import { writeFileSync } from "node:fs";
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
const ids = ["author-error", "author-ignores", "author-never", "author-held"];
const usage = () => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } });
const aborted = (signal) => new Promise((resolve) => { if (signal.aborted) resolve(); else signal.addEventListener("abort", () => resolve(), { once: true }); });
const later = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
function streamProbe(model, context, options) {
  const output = { role: "assistant", content: [], api: model.api, provider: model.provider, model: model.id, usage: usage(), stopReason: "stop", timestamp: Date.now() };
  const block = { type: "text", text: "" };
  const stream = createAssistantMessageEventStream();
  const delta = (piece) => { block.text += piece; stream.push({ type: "text_delta", contentIndex: 0, delta: piece, partial: output }); };
  (async () => {
    stream.push({ type: "start", partial: output });
    output.content.push(block);
    stream.push({ type: "text_start", contentIndex: 0, partial: output });
    delta("alpha ");
    await later(5);
    delta("beta ");
    await aborted(options.signal);
    writeFileSync("@SEEN@", "abort");
    if (model.id === "author-error") {
      output.stopReason = "aborted";
      output.errorMessage = "the author saw the abort";
      stream.push({ type: "error", reason: "aborted", error: output });
      stream.end();
    } else if (model.id === "author-ignores") {
      await later(300);
      delta("gamma ");
      stream.push({ type: "text_end", contentIndex: 0, content: block.text, partial: output });
      stream.push({ type: "done", reason: "stop", message: output });
      stream.end();
    } else if (model.id === "author-held") {
      setInterval(() => {}, 1 << 30);
    }
  })();
  return stream;
}
export default function (pi) {
  pi.registerProvider("abort-probe", {
    baseUrl: "http://localhost:0", apiKey: "unused", api: "abort-probe-api",
    models: ids.map((id) => ({ id, name: id, api: "abort-probe-api", provider: "abort-probe", baseUrl: "http://localhost:0", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 })),
    streamSimple: streamProbe,
  });
}
`

// quitProbeShutdownNever is a Node extension whose session_shutdown handler never settles and holds no timer and no I/O.
const quitProbeShutdownNever = `export default function (pi) {
  pi.on("session_shutdown", () => new Promise(() => {}));
}
`

// quitProbeGoModule is the go.mod of the Go probe; the argument is the directory of this tree's Go extension SDK.
const quitProbeGoModule = `module provider-abort-go

go 1.26

require github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0

replace github.com/MichaelKinsy/PiG/extensions/sdk => %s
`

// quitProbeGo is quitProbeNode written with the Go extension SDK.
const quitProbeGo = `package providerabortgo

import (
	"os"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	ext := sdk.New("provider-abort-go")
	models := []any{}
	for _, id := range []string{"author-error", "author-ignores", "author-never"} {
		models = append(models, map[string]any{"id": id, "name": id, "api": "abort-probe-api", "provider": "abort-probe", "baseUrl": "http://localhost:0", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096})
	}
	ext.RegisterProvider("abort-probe", sdk.ProviderConfig{"baseUrl": "http://localhost:0", "apiKey": "unused", "api": "abort-probe-api", "models": models, "streamSimple": streamProbe})
	return ext
}

func streamProbe(ctx sdk.Context, model, request, _ map[string]any) (*sdk.ModelEventStream, error) {
	id, _ := model["id"].(string)
	cost := map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}
	output := map[string]any{"role": "assistant", "content": []any{}, "api": model["api"], "provider": model["provider"], "model": id, "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": cost}, "stopReason": "stop", "timestamp": time.Now().UnixMilli()}
	block := map[string]any{"type": "text", "text": ""}
	stream := sdk.CreateAssistantMessageEventStream()
	delta := func(piece string) {
		text, _ := block["text"].(string)
		block["text"] = text + piece
		stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": piece, "partial": output})
	}
	go func() {
		stream.Push(map[string]any{"type": "start", "partial": output})
		output["content"] = []any{block}
		stream.Push(map[string]any{"type": "text_start", "contentIndex": 0, "partial": output})
		delta("alpha ")
		time.Sleep(5 * time.Millisecond)
		delta("beta ")
		<-ctx.Done()
		_ = os.WriteFile("@SEEN@", []byte("abort"), 0o600)
		switch id {
		case "author-error":
			output["stopReason"] = "aborted"
			output["errorMessage"] = "the author saw the abort"
			stream.Push(map[string]any{"type": "error", "reason": "aborted", "error": output})
		case "author-ignores":
			time.Sleep(300 * time.Millisecond)
			delta("gamma ")
			stream.Push(map[string]any{"type": "text_end", "contentIndex": 0, "content": block["text"], "partial": output})
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": output})
		}
	}()
	return stream, nil
}
`

// quitProbePython is quitProbeNode written with the Python extension SDK.
const quitProbePython = `import threading
import time

import pig_sdk

IDS = ["author-error", "author-ignores", "author-never"]
COST = {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}


def new_extension() -> pig_sdk.Extension:
    extension = pig_sdk.Extension("provider-abort-python")

    def stream_probe(ctx, model, request, options):
        block = {"type": "text", "text": ""}
        message = {"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": [block], "stopReason": "stop", "timestamp": 1,
                   "usage": {**COST, "totalTokens": 0, "cost": {**COST, "total": 0}}}
        result = pig_sdk.ModelEventStream()

        def delta(piece):
            block["text"] += piece
            result.push({"type": "text_delta", "contentIndex": 0, "delta": piece, "partial": message})

        def produce():
            result.push({"type": "start", "partial": message})
            result.push({"type": "text_start", "contentIndex": 0, "partial": message})
            delta("alpha ")
            time.sleep(0.005)
            delta("beta ")
            while not ctx.is_cancelled():
                time.sleep(0.005)
            with open("@SEEN@", "w") as seen:
                seen.write("abort")
            if model["id"] == "author-error":
                message["stopReason"] = "aborted"
                message["errorMessage"] = "the author saw the abort"
                result.push({"type": "error", "reason": "aborted", "error": message})
            elif model["id"] == "author-ignores":
                time.sleep(0.3)
                delta("gamma ")
                result.push({"type": "text_end", "contentIndex": 0, "content": block["text"], "partial": message})
                result.push({"type": "done", "reason": "stop", "message": message})

        threading.Thread(target=produce, daemon=True).start()
        return result

    extension.register_provider("abort-probe", {"api": "abort-probe-api", "baseUrl": "http://localhost:0", "apiKey": "unused", "streamSimple": stream_probe,
                                                "models": [{"id": id, "name": id, "reasoning": False, "input": ["text"], "cost": COST, "contextWindow": 128000, "maxTokens": 4096} for id in IDS]})
    return extension
`

// quitProbeTimerSibling is a second Node extension that holds a timer from the moment it is loaded.
const quitProbeTimerSibling = `export default function (pi) {
  setInterval(() => {}, 1 << 30);
  pi.registerCommand("timer-sibling", { description: "nothing", handler: async () => {} });
}
`

// quitProbeIdleGo is a Go extension that registers nothing and does nothing.
const quitProbeIdleGo = `package idlego

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension { return sdk.New("idle-go") }
`

// quitPane is one pig in interactive mode in the pane of a tmux server of its own. The pane stays when its program exits, so the exit status can be read.
type quitPane struct {
	t      *testing.T
	socket string
	stderr string
	// exit is the file in which the pane's shell writes pig's exit status; pig is the process ID of pig once pid has read it.
	exit string
	pig  int
}

func startQuitPane(t *testing.T, cwd, stderr string, env []string, argv ...string) *quitPane {
	t.Helper()
	dir := testenv.ShortTempDir(t, "pig-quit-")
	p := &quitPane{t: t, socket: filepath.Join(dir, "tmux"), stderr: stderr, exit: filepath.Join(dir, "exit-status")}
	// pig runs as the child of a shell that records its exit status as a user's shell sees it: 128 plus the signal's number when a signal ended pig. tmux's own status of
	// the pane is not read: tmux marks a pane dead when its terminal closes, and that can be long before it has collected the status of the pane's process (current
	// behavior of tmux, seen for Pi and for pig alike). After pig exited the shell prints quitExitedMark on the terminal, which ends what pig wrote. The shell traps SIGINT so that a ctrl+c at the stopped TUI, which the terminal sends to it too, does not end it.
	script := "trap : INT\nTERM=xterm-256color"
	for _, arg := range argv {
		script += " " + quitShellQuote(arg)
	}
	script += " 2>" + quitShellQuote(stderr) + "\necho $? > " + quitShellQuote(p.exit+".tmp") + "\nmv " + quitShellQuote(p.exit+".tmp") + " " + quitShellQuote(p.exit) + "\nprintf " + quitExitedMark + "\n"
	runner := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(runner, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command := "exec sh " + quitShellQuote(runner)
	args := []string{"new-session", "-d", "-s", "keeper", "sleep 100000", ";", "set-option", "-g", "remain-on-exit", "on", ";", "new-session", "-d", "-s", "pig", "-x", "100", "-y", "32", "-c", cwd}
	// The color level of pig (chalk's) reads FORCE_COLOR, CI and the terminal's own variables, which the pane would inherit from whoever runs the test; the quit rows
	// match bytes, so the pane pins the level to zero (see quitBytesAfterStop).
	args = append(args, "-e", "FORCE_COLOR=0")
	for _, assignment := range env {
		args = append(args, "-e", assignment)
	}
	if out, err := p.tmux(append(args, command)...); err != nil {
		t.Fatalf("tmux: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := p.tmux("list-panes", "-t", "pig", "-F", "#{pane_dead} #{pane_pid}"); err == nil {
			if fields := strings.Fields(out); len(fields) == 2 && fields[0] == "0" {
				if pid, err := strconv.Atoi(fields[1]); err == nil {
					killProcessTree(pid)
				}
			}
		}
		_, _ = p.tmux("kill-server")
	})
	return p
}

// quitExitedMark is what the pane's shell prints on the terminal once pig has exited.
const quitExitedMark = "PIG-HAS-EXITED"

// quitShellQuote quotes arg for sh.
func quitShellQuote(arg string) string { return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'" }

// killProcessTree kills pid and every descendant of it.
func killProcessTree(pid int) {
	for _, child := range childProcesses(pid) {
		killProcessTree(child)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// descendantProcesses lists every descendant of pid.
func descendantProcesses(pid int) []int {
	var all []int
	for _, child := range childProcesses(pid) {
		all = append(append(all, child), descendantProcesses(child)...)
	}
	return all
}

// processState describes a process for a failure message: its state, its parent and its threads, from /proc.
func processState(pid int) string {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return fmt.Sprintf("process %d: gone", pid)
	}
	var lines []string
	for line := range strings.SplitSeq(string(status), "\n") {
		if strings.HasPrefix(line, "State:") || strings.HasPrefix(line, "PPid:") || strings.HasPrefix(line, "Threads:") {
			lines = append(lines, strings.Join(strings.Fields(line), " "))
		}
	}
	return fmt.Sprintf("process %d: %s", pid, strings.Join(lines, "; "))
}

func (p *quitPane) tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", append([]string{"-S", p.socket, "-f", "/dev/null"}, args...)...).CombinedOutput()
	return string(out), err
}

func (p *quitPane) must(args ...string) string {
	p.t.Helper()
	out, err := p.tmux(args...)
	if err != nil {
		p.t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return out
}

func (p *quitPane) screen() string { return p.must("capture-pane", "-p", "-t", "pig") }

// keys sends named keys (C-c, Escape, Enter); text sends literal text.
func (p *quitPane) keys(names ...string) {
	p.must(append([]string{"send-keys", "-t", "pig"}, names...)...)
}
func (p *quitPane) text(literal string) { p.must("send-keys", "-t", "pig", "-l", literal) }

// pid is the process ID of pig: the child of the pane's shell.
func (p *quitPane) pid() int {
	p.t.Helper()
	shell, err := strconv.Atoi(strings.TrimSpace(p.must("list-panes", "-t", "pig", "-F", "#{pane_pid}")))
	if err != nil {
		p.t.Fatal(err)
	}
	p.await("pig under the pane's shell", func() bool {
		if children := childProcesses(shell); len(children) > 0 {
			p.pig = children[0]
		}
		return p.pig != 0
	})
	return p.pig
}

// status reports whether pig has exited and the status its shell saw: pig's exit status, or 128 plus the number of the signal that ended it.
func (p *quitPane) status() (int, bool) {
	p.t.Helper()
	data, err := os.ReadFile(p.exit)
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		p.t.Fatal(err)
	}
	return code, true
}

// cooked reports whether the pane's terminal is in canonical mode, which pig restores when it stops its TUI.
func (p *quitPane) cooked() bool {
	p.t.Helper()
	tty, err := os.OpenFile(strings.TrimSpace(p.must("list-panes", "-t", "pig", "-F", "#{pane_tty}")), os.O_RDONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = tty.Close() }()
	termios, err := unix.IoctlGetTermios(int(tty.Fd()), unix.TCGETS)
	if err != nil {
		p.t.Fatal(err)
	}
	return termios.Lflag&unix.ICANON != 0
}

// await polls until done reports true. The event decides when the wait ends; a hang fails after the test budget with the screen and pig's stderr.
func (p *quitPane) await(what string, done func() bool) {
	p.t.Helper()
	budget := testbudget.Wait(p.t)
	deadline := time.Now().Add(budget)
	for !done() {
		if time.Now().After(deadline) {
			stderr, _ := os.ReadFile(p.stderr)
			p.t.Fatalf("%s: not within %s\n--- pig ---\n%s\n--- screen ---\n%s\n--- stderr ---\n%s", what, budget, processState(p.pig), p.screen(), stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// childProcesses lists the processes whose parent is pid, from /proc.
func childProcesses(pid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var children []int
	for _, entry := range entries {
		child, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		// The fields after the command name, which is in parentheses and may hold spaces: state, then the parent's process ID.
		fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if len(fields) > 1 && fields[1] == strconv.Itoa(pid) {
			children = append(children, child)
		}
	}
	return children
}

// quitProbeExtensions writes the probe of the author's kind below root and returns the -e arguments that load it.
func quitProbeExtensions(t *testing.T, root, author, seen string) []string {
	t.Helper()
	write := func(path, content string) string {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(content, "@SEEN@", seen)), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	switch author {
	case "go":
		sdkDir, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(root, "provider-abort-go")
		write(filepath.Join(source, "go.mod"), fmt.Sprintf(quitProbeGoModule, sdkDir))
		write(filepath.Join(source, "extension.go"), quitProbeGo)
		return []string{"-e", source}
	case "python":
		if _, err := exec.LookPath("python3"); err != nil {
			t.Skip("python3 is not on PATH:", err)
		}
		source := filepath.Join(root, "provider-abort-python")
		write(filepath.Join(source, "main.py"), quitProbePython)
		return []string{"-e", source}
	case "node-packed":
		return []string{"-e", write(filepath.Join(root, "provider-abort.mjs"), quitProbeNode), "-e", write(filepath.Join(root, "provider-abort-sibling.mjs"), providerStreamAbortSibling)}
	case "node-packed-timer":
		return []string{"-e", write(filepath.Join(root, "provider-abort.mjs"), quitProbeNode), "-e", write(filepath.Join(root, "timer-sibling.mjs"), quitProbeTimerSibling)}
	case "node-and-idle-go":
		sdkDir, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(root, "idle-go")
		write(filepath.Join(source, "go.mod"), strings.Replace(fmt.Sprintf(quitProbeGoModule, sdkDir), "module provider-abort-go", "module idle-go", 1))
		write(filepath.Join(source, "extension.go"), quitProbeIdleGo)
		return []string{"-e", write(filepath.Join(root, "provider-abort.mjs"), quitProbeNode), "-e", source}
	case "node-shutdown-never":
		return []string{"-e", write(filepath.Join(root, "provider-abort.mjs"), quitProbeNode), "-e", write(filepath.Join(root, "shutdown-never.mjs"), quitProbeShutdownNever)}
	}
	return []string{"-e", write(filepath.Join(root, "provider-abort.mjs"), quitProbeNode)}
}

// quitPersisted is providerAbortMessage of each assistant message in the session files under sessions.
func quitPersisted(t *testing.T, sessions string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	persisted := []string{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			var entry struct {
				Type    string         `json:"type"`
				Message map[string]any `json:"message"`
			}
			if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "message" && entry.Message["role"] == "assistant" {
				persisted = append(persisted, providerAbortMessage(entry.Message))
			}
		}
	}
	return persisted
}

// TestInteractiveQuitWithAnExtensionProviderTurnInFlight: the user quits interactive mode (ctrl+c twice, ctrl+d or /quit) while a turn served by an extension's
// streamSimple has not ended. Pi's quit stops the TUI and then awaits runtimeHost.dispose(), which awaits session.abort() first (interactive-mode.ts shutdown,
// agent-session-runtime.ts dispose), with no bound. Pi 1.5.2, run under tmux with the Node probe, gives:
//
//   - an author that never reacts to the abort (Escape, then the quit): the process exits 0 at once (0.5 s, 0.1 s and 0.4 s for the three keys), with no resume hint,
//     because stop() took stdin out of the event loop and the author's stream holds no timer and no I/O: nothing keeps the loop alive. Nothing of the turn is persisted.
//     pig did not exit (10 s watched), and its runtime process stayed: the dispose waited for the run, and nothing told the runtime that input had ended.
//   - a session_shutdown handler that never settles ends the same way: the loop is empty under the pending dispose.
//   - an author that answers the quit's abort (an error event with its partial), or that goes on for 0.3 s and completes, ends the turn: the message is persisted as the
//     author's stream ended it, and the process exits 0.
//
// The exit status is the one the shell that runs pig sees (startQuitPane).
//
// A Go or Python author's runtime has no event loop that could report that nothing can make progress, so an author of such an extension that never reacts holds the quit,
// as a Node author whose loop is alive holds Pi's (Pi with a never-reacting author that holds a timer does not exit either, 10 s watched; ctrl+c at the stopped TUI ends
// it). The rows pin that: pig waits with its TUI stopped, and ctrl+c ends it with status 130. An exit rule for those runtimes is an open question of the owner.
//
// After every exit no child process of pig is alive.
func TestInteractiveQuitWithAnExtensionProviderTurnInFlight(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH:", err)
	}
	binary := buildPigBinaryForSignalTest(t)
	type row struct {
		author, model, key string
		// escape: Escape aborts the turn, and the abort reaches the author, before the quit; otherwise the quit's own dispose aborts it.
		escape bool
		// held: the quit waits for the author; ctrl+c at the stopped TUI ends pig with 130.
		held bool
		// idle: no prompt is sent.
		idle      bool
		persisted []string
	}
	completed := []string{providerAbortWant("alpha beta gamma ", "stop", "")}
	answered := []string{providerAbortWant("alpha beta ", "aborted", "the author saw the abort")}
	rows := []row{
		{author: "node-shutdown-never", model: "author-never", key: "ctrl-d", idle: true},
		{author: "node", model: "author-ignores", key: "ctrl-c-twice", persisted: completed},
		{author: "node", model: "author-ignores", key: "quit", persisted: completed},
	}
	for _, author := range []string{"node", "node-packed"} {
		for _, key := range []string{"ctrl-c-twice", "ctrl-d", "quit"} {
			rows = append(rows, row{author: author, model: "author-never", key: key, escape: true})
		}
	}
	for _, author := range []string{"go", "python"} {
		rows = append(rows, row{author: author, model: "author-never", key: "ctrl-d", escape: true, held: true})
	}
	// A Node author that never reacts and holds a timer: its loop is alive, so it holds the quit as it holds Pi's (Pi 1.5.2 does not exit, 10 s watched), and the ctrl+c
	// that ends pig must not leave its runtime behind.
	// Mixed sets. A second Node extension that holds a timer is packed into the author's runtime process: its timer keeps that loop alive, as it keeps Pi's one loop
	// alive, so the quit waits. An idle Go extension beside the Node author has nothing in flight and does not hold the quit.
	rows = append(rows,
		row{author: "node", model: "author-held", key: "ctrl-d", escape: true, held: true},
		row{author: "node-packed-timer", model: "author-never", key: "ctrl-d", escape: true, held: true},
		row{author: "node-and-idle-go", model: "author-never", key: "ctrl-d", escape: true},
	)
	for _, author := range []string{"node", "node-packed", "go", "python"} {
		rows = append(rows, row{author: author, model: "author-ignores", key: "ctrl-d", persisted: completed}, row{author: author, model: "author-error", key: "ctrl-d", persisted: answered})
	}
	for _, tc := range rows {
		t.Run(tc.author+"/"+tc.model+"/"+tc.key, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			agent, project, sessions, seen := filepath.Join(root, "agent"), filepath.Join(root, "project"), filepath.Join(root, "sessions"), filepath.Join(root, "abort-seen")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			seedFirstRunDone(t, agent)
			argv := append([]string{binary, "--no-extensions", "--model", "abort-probe/" + tc.model, "--session-dir", sessions}, quitProbeExtensions(t, root, tc.author, seen)...)
			p := startQuitPane(t, project, filepath.Join(root, "stderr.log"), []string{"PIG_HOME=" + root, "PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent, "PIG_OFFLINE=1", "PI_OFFLINE=1"}, argv...)
			p.await("the model in the footer", func() bool { return strings.Contains(p.screen(), tc.model) })
			if !tc.idle {
				p.text("HOLD the stream")
				p.await("the typed prompt", func() bool { return strings.Contains(p.screen(), "HOLD the stream") })
				p.keys("Enter")
				p.await("the partial text", func() bool { return strings.Contains(p.screen(), "alpha beta") })
			}
			pig := p.pid()
			children := childProcesses(pig)
			if len(children) == 0 {
				t.Fatalf("pig %d has no extension process", pig)
			}
			if tc.escape {
				p.keys("Escape")
				p.await("the abort at the author", func() bool { _, err := os.Stat(seen); return err == nil })
			}
			switch tc.key {
			case "ctrl-c-twice":
				p.keys("C-c", "C-c")
			case "ctrl-d":
				p.keys("C-d")
			case "quit":
				p.text("/quit")
				p.await("the typed /quit", func() bool { return strings.Contains(p.screen(), "/quit") })
				p.keys("Enter")
			}
			wantStatus := 0
			if tc.held {
				// ctrl+c at the stopped TUI ends Pi by the signal itself (its stop() removed its handlers), which a shell reports as 130; pig exits 130 from its handler. That it does so
				// at once (Pi: 130 after 28 to 32 ms under tmux) and does not spend a second on the lock that its pending dispose holds is pinned without a clock by
				// TestInterruptOfAPendingQuitDoesNotWaitForItsLock (internal/codingagent). Here no time decides: pig's exit waits for its killed extension processes to be reaped
				// (D19), which takes as long as the load makes it, so the exit is awaited up to the hang bound, and the author is never released.
				wantStatus = 130
				p.await("the stopped TUI", p.cooked)
				if status, exited := p.status(); exited {
					t.Fatalf("pig exited by itself (status %d) while its author's runtime, which cannot report an idle loop, had not answered", status)
				}
				p.keys("C-c")
			}
			status := 0
			p.await("pig's exit after "+tc.key, func() bool {
				code, exited := p.status()
				status = code
				return exited
			})
			if status != wantStatus {
				t.Errorf("pig exited with status %d after %s, want %d", status, tc.key, wantStatus)
			}
			budget := testbudget.Wait(t)
			deadline := time.Now().Add(budget)
			for _, child := range children {
				for !errors.Is(syscall.Kill(child, 0), syscall.ESRCH) {
					if time.Now().After(deadline) {
						t.Errorf("the child process %d of pig is alive %s after pig exited", child, budget)
						_ = syscall.Kill(child, syscall.SIGKILL)
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			if got := quitPersisted(t, sessions); strings.Join(got, "\n") != strings.Join(tc.persisted, "\n") {
				t.Errorf("the session files hold the assistant messages %q, want %q", got, tc.persisted)
			}
			if tc.persisted == nil && strings.Contains(p.screen(), "To resume this session") {
				t.Errorf("pig printed the resume hint of a completed dispose, though the dispose never returned:\n%s", p.screen())
			}
		})
	}
}

// quitEventRecorder is a Node extension that appends the events of a turn's end and of the quit to the file @EVENTS@, one line each.
const quitEventRecorder = `import { appendFileSync } from "node:fs";
export default function (pi) {
  const log = (line) => appendFileSync("@EVENTS@", line + "\n");
  pi.on("message_end", (event) => { if (event.message?.role === "assistant") log("message_end " + event.message.stopReason); });
  for (const name of ["turn_end", "agent_end"]) pi.on(name, () => log(name));
  pi.on("agent_settled", async () => {
    await new Promise((resolve) => setTimeout(resolve, 1000));
    log("agent_settled");
  });
  pi.on("session_shutdown", async (event) => {
    await new Promise((resolve) => setTimeout(resolve, 200));
    log("session_shutdown " + event.reason);
  });
}
`

// quitBytesAfterStop is what pig writes to its terminal after the stop sequence of its TUI (it ends by turning bracketed paste off) at a quit by keys: the resume
// hint, after at most a title, and then the title, progress-off and cursor sequences of its exit. Nothing of the run that ends under the dispose is among them: no
// text, and no progress sequence before the hint (Pi's stop() turns the terminal progress off before the TUI stops, and its mode is unsubscribed from the run).
// The hint is plain because startQuitPane pins FORCE_COLOR=0 (chalk level 0): Pi prints it as chalk.dim, which adds the dim sequences at any nonzero level, and
// pig's chalk level follows Pi's, so without the pin the bytes depend on the environment of the test (CI set without a known CI provider gives level 0, a TTY under
// TERM=xterm-256color without CI gives level 2).
var quitBytesAfterStop = regexp.MustCompile(`^(\x1b\]0;[^\x07]*\x07)?To resume this session:\r\n[^\r\n]*\r\n(\x1b\]9;4;0\x07|\x1b\]0;[^\x07]*\x07|\x1b\[\?25h)*$`)

// TestInteractiveQuitWhileABuiltInProviderTurnStreams: the user quits interactive mode (ctrl+c twice, ctrl+d or /quit) while a turn of a built-in provider kind streams:
// openai-completions through models.json, served by a local listener that shows "alpha beta " and then holds its stream open. No Escape precedes the quit: the quit's own
// dispose aborts the turn (interactive-mode.ts shutdown: this.stop(), then `await this.runtimeHost.dispose()`, whose first step is `await this.session.abort()`).
//
// Pi 1.5.2, run under tmux against the same listener, exits 0 within 0.6 s for each key. Its session file ends with the assistant message "alpha beta ", stopReason
// aborted, errorMessage "Request was aborted"; an extension sees message_end, turn_end, agent_end, agent_settled and then session_shutdown; the provider's connection is
// closed; the resume hint is printed, and nothing of the run is drawn after the TUI stopped (stop() unsubscribes the mode from the Session), so the screen never shows
// the mode's "Operation aborted". The rows run with the terminal progress setting on, which makes an agent_end that the mode applied after its stop visible in the
// terminal's bytes (the progress sequence), with an agent_settled handler that takes a second, which the quit's abort awaits before session_shutdown as Pi's does
// (agent-session.ts _emitAgentSettled), and with a session_shutdown handler that takes 200 ms, which the dispose awaits.
//
// pig did not exit (10 s watched): its dispose awaited the aborted run on the owner loop, and the run's event flush waited for that loop.
func TestInteractiveQuitWhileABuiltInProviderTurnStreams(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH:", err)
	}
	binary := buildPigBinaryForSignalTest(t)
	for _, key := range []string{"ctrl-c-twice", "ctrl-d", "quit"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			agent, project, sessions, events := filepath.Join(root, "agent"), filepath.Join(root, "project"), filepath.Join(root, "sessions"), filepath.Join(root, "events")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(agent, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"terminal":{"showTerminalProgress":true}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			raw := filepath.Join(root, "terminal-bytes")
			closed := make(chan struct{})
			var once sync.Once
			listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				for _, delta := range []string{`{"role":"assistant","content":""}`, `{"content":"alpha "}`, `{"content":"beta "}`} {
					_, _ = fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"hold-model\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":null}]}\n\n", delta)
					w.(http.Flusher).Flush()
				}
				// The stream stays open until the client closes it.
				<-r.Context().Done()
				once.Do(func() { close(closed) })
			}))
			t.Cleanup(listener.Close)
			models := fmt.Sprintf(`{"providers":{"localhold":{"baseUrl":%q,"api":"openai-completions","apiKey":"dummy","models":[{"id":"hold-model"}]}}}`, listener.URL+"/v1")
			if err := os.WriteFile(filepath.Join(agent, "models.json"), []byte(models), 0o600); err != nil {
				t.Fatal(err)
			}
			recorder := filepath.Join(root, "recorder.mjs")
			if err := os.WriteFile(recorder, []byte(strings.ReplaceAll(quitEventRecorder, "@EVENTS@", events)), 0o600); err != nil {
				t.Fatal(err)
			}
			argv := []string{binary, "--no-extensions", "-e", recorder, "--model", "localhold/hold-model", "--session-dir", sessions}
			p := startQuitPane(t, project, filepath.Join(root, "stderr.log"), []string{"PIG_HOME=" + root, "PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent, "PIG_OFFLINE=1", "PI_OFFLINE=1"}, argv...)
			p.must("pipe-pane", "-t", "pig", "-o", "cat > "+quitShellQuote(raw))
			p.await("the model in the footer", func() bool { return strings.Contains(p.screen(), "hold-model") })
			p.text("HOLD the stream")
			p.await("the typed prompt", func() bool { return strings.Contains(p.screen(), "HOLD the stream") })
			p.keys("Enter")
			p.await("the partial text", func() bool { return strings.Contains(p.screen(), "alpha beta") })
			pig := p.pid()
			children := childProcesses(pig)
			if len(children) == 0 {
				t.Fatalf("pig %d has no extension process", pig)
			}
			switch key {
			case "ctrl-c-twice":
				p.keys("C-c", "C-c")
			case "ctrl-d":
				p.keys("C-d")
			case "quit":
				p.text("/quit")
				p.await("the typed /quit", func() bool { return strings.Contains(p.screen(), "/quit") })
				p.keys("Enter")
			}
			status := 0
			p.await("pig's exit after "+key+" while the turn streams", func() bool {
				code, exited := p.status()
				status = code
				return exited
			})
			if status != 0 {
				t.Errorf("pig exited with status %d after %s, want 0", status, key)
			}
			budget := testbudget.Wait(t)
			deadline := time.Now().Add(budget)
			for _, child := range children {
				for !errors.Is(syscall.Kill(child, 0), syscall.ESRCH) {
					if time.Now().After(deadline) {
						t.Errorf("the child process %d of pig is alive %s after pig exited", child, budget)
						_ = syscall.Kill(child, syscall.SIGKILL)
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			p.await("the end of the provider's connection", func() bool {
				select {
				case <-closed:
					return true
				default:
					return false
				}
			})
			if got, want := quitPersisted(t, sessions), []string{providerAbortWant("alpha beta ", "aborted", "Request was aborted")}; strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("the session files hold the assistant messages %q, want %q", got, want)
			}
			seen, _ := os.ReadFile(events)
			if want := "message_end aborted\nturn_end\nagent_end\nagent_settled\nsession_shutdown quit\n"; string(seen) != want {
				t.Errorf("the extension saw %q, want %q", seen, want)
			}
			screen := p.screen()
			if !strings.Contains(screen, "To resume this session") {
				t.Errorf("pig did not print the resume hint of a completed dispose:\n%s", screen)
			}
			var written []byte
			p.await("the terminal's bytes up to pig's exit", func() bool {
				written, _ = os.ReadFile(raw)
				return bytes.Contains(written, []byte(quitExitedMark))
			})
			written, _, _ = bytes.Cut(written, []byte(quitExitedMark))
			stop := []byte("\x1b[?2004l")
			if at := bytes.LastIndex(written, stop); at < 0 {
				t.Errorf("the terminal's bytes do not hold the stop sequence of the TUI: %q", written[max(0, len(written)-400):])
			} else if after := written[at+len(stop):]; !quitBytesAfterStop.Match(after) {
				t.Errorf("after its TUI stopped pig wrote %q, want only the resume hint and the sequences of the exit", after)
			}
			if strings.Contains(screen, "Operation aborted") {
				t.Errorf("pig drew the aborted turn after its TUI stopped:\n%s", screen)
			}
		})
	}
}

// TestInteractiveQuitDoesNotJoinAUsersBashCommand: the user quits while a `!` command runs. Pi's dispose awaits session.abort(), which aborts the agent's run and does not
// wait for a user's bash command (agent-session.ts abort), so the quit ends the process at once; pig's settle of the run for the quit does not join it either.
func TestInteractiveQuitDoesNotJoinAUsersBashCommand(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH:", err)
	}
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	agent, project, sessions := filepath.Join(root, "agent"), filepath.Join(root, "project"), filepath.Join(root, "sessions")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	seedFirstRunDone(t, agent)
	// No request is sent: the model only has to exist.
	models := `{"providers":{"localhold":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"dummy","models":[{"id":"hold-model"}]}}}`
	if err := os.WriteFile(filepath.Join(agent, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	argv := []string{binary, "--no-extensions", "--model", "localhold/hold-model", "--session-dir", sessions}
	p := startQuitPane(t, project, filepath.Join(root, "stderr.log"), []string{"PIG_HOME=" + root, "PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent, "PIG_OFFLINE=1", "PI_OFFLINE=1"}, argv...)
	p.await("the model in the footer", func() bool { return strings.Contains(p.screen(), "hold-model") })
	pig := p.pid()
	p.text("!sleep 600")
	p.await("the typed command", func() bool { return strings.Contains(p.screen(), "sleep 600") })
	p.keys("Enter")
	var running []int
	p.await("the user's command running under pig", func() bool {
		running = descendantProcesses(pig)
		for _, pid := range running {
			if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil && strings.HasPrefix(string(cmdline), "sleep\x00600") {
				return true
			}
		}
		return false
	})
	t.Cleanup(func() {
		for _, pid := range running {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	p.keys("C-d")
	status := 0
	p.await("pig's exit after ctrl-d while the user's command runs", func() bool {
		code, exited := p.status()
		status = code
		return exited
	})
	if status != 0 {
		t.Errorf("pig exited with status %d, want 0", status)
	}
}
