//go:build integration && !windows

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// deadTerminalRuns is how many terminals each setup of TestDeadTerminalEmitsSessionShutdownToExtensions kills: which of the
// hangup's events reaches pig first (the input's end, the shell's SIGHUP, a failed write) is a race, so one run proves little.
const deadTerminalRuns = 20

// deadTerminalShutdownBound is how long pig's emergency exit waits for the session_shutdown handlers (shutdownExtensionsBriefly).
const deadTerminalShutdownBound = time.Second

// deadTerminalHandlerWait is how long a fixture's handler waits between its two marker lines unless a test sets another wait.
const deadTerminalHandlerWait = 50 * time.Millisecond

// deadTerminalProbeExtension is the Node fixture; testdata/dead-terminal/deadterm is the same extension on the Go SDK, and
// deadTerminalPythonExtension, deadTerminalRustExtension and deadTerminalLispExtension are it on the Python, Rust and Lisp
// SDKs. Each
// appends "ready pid=<its process>" at session_start and, at session_shutdown, the reason before and after a wait of
// PIG_DEAD_TERMINAL_WAIT_MS that the host has to sit through, to the file PIG_DEAD_TERMINAL_MARKER names.
const deadTerminalProbeExtension = `import { appendFileSync } from "node:fs";
const mark = (line) => appendFileSync(process.env.PIG_DEAD_TERMINAL_MARKER, line + "\n");
export default function (pi) {
  pi.on("session_start", () => mark("ready pid=" + process.pid));
  pi.on("session_shutdown", async (event) => {
    mark("session_shutdown reason=" + event.reason);
    await new Promise((resolve) => setTimeout(resolve, Number(process.env.PIG_DEAD_TERMINAL_WAIT_MS)));
    mark("session_shutdown settled reason=" + event.reason);
  });
}
`

const deadTerminalPythonExtension = `import os
import time

import pig_sdk


def mark(line):
    with open(os.environ["PIG_DEAD_TERMINAL_MARKER"], "a") as marker:
        marker.write(line + "\n")


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("deadterm_py")

    def started(ctx, data):
        mark(f"ready pid={os.getpid()}")

    def shutdown(ctx, data):
        reason = data.get("reason", "")
        mark(f"session_shutdown reason={reason}")
        time.sleep(int(os.environ["PIG_DEAD_TERMINAL_WAIT_MS"]) / 1000)
        mark(f"session_shutdown settled reason={reason}")

    ext.on_event("session_start", started)
    ext.on_event("session_shutdown", shutdown)
    return ext
`

const deadTerminalRustCargo = `[package]
name = "deadterm-rs"
version = "0.1.0"
edition = "2024"

[dependencies]
# Pig resolves this dependency to the version-matched staged SDK at build time.
pig-sdk = "0.1.0"
serde_json = "1"
`

const deadTerminalRustExtension = `use pig_sdk::Extension;
use serde_json::Value;
use std::io::Write;

fn mark(line: &str) {
    let Ok(path) = std::env::var("PIG_DEAD_TERMINAL_MARKER") else { return };
    if let Ok(mut marker) = std::fs::OpenOptions::new().append(true).create(true).open(path) {
        let _ = writeln!(marker, "{line}");
    }
}

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("deadterm-rs");
    ext.on_event("session_start", |_ctx, _data| {
        mark(&format!("ready pid={}", std::process::id()));
        None
    });
    ext.on_event("session_shutdown", |_ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("").to_string();
        let wait = std::env::var("PIG_DEAD_TERMINAL_WAIT_MS").ok().and_then(|ms| ms.parse().ok()).unwrap_or(0);
        mark(&format!("session_shutdown reason={reason}"));
        std::thread::sleep(std::time::Duration::from_millis(wait));
        mark(&format!("session_shutdown settled reason={reason}"));
        None
    });
    ext
}
`

const deadTerminalLispSystem = `(asdf:defsystem "deadterm-lisp"
  :depends-on ("pig-sdk")
  :serial t
  :components ((:file "deadterm-lisp")))
`

const deadTerminalLispExtension = `(defpackage "deadterm-lisp" (:use "COMMON-LISP"))
(in-package "deadterm-lisp")

(defun mark (line)
  (with-open-file (marker (sb-ext:posix-getenv "PIG_DEAD_TERMINAL_MARKER")
                          :direction :output :if-exists :append :if-does-not-exist :create)
    (write-line line marker)))

(pig:define-extension "deadterm-lisp"
  (:on "session_start"
       (lambda (ctx data)
         (declare (ignore ctx data))
         (mark (format nil "ready pid=~D" (sb-unix:unix-getpid)))
         nil))
  (:on "session_shutdown"
       (lambda (ctx data)
         (declare (ignore ctx))
         (let ((reason (pig:jget data "reason")))
           (mark (format nil "session_shutdown reason=~A" reason))
           (sleep (/ (parse-integer (sb-ext:posix-getenv "PIG_DEAD_TERMINAL_WAIT_MS")) 1000))
           (mark (format nil "session_shutdown settled reason=~A" reason)))
         nil)))
`

// deadTerminalSession is one interactive pig whose terminal a test kills.
type deadTerminalSession struct {
	bin, home, cwd string
	args           []string
	// members is how many extensions write to the marker.
	members int
	// recordStatus runs pig under a shell that ignores SIGHUP and records pig's exit status; without it pig runs under an
	// interactive shell, which dies of the hangup as a user's does.
	recordStatus bool
	// working kills the terminal while a turn is in flight (the faux provider's bash call that sleeps).
	working bool
	// handlerWait is how long the fixture's handler waits between its two marker lines. A wait above deadTerminalShutdownBound
	// is one pig cuts: the second line is then missing and the exit comes at the bound.
	handlerWait time.Duration
	// cell is the language of the packed cell that hosts each extension: its process is that cell's runner in the agent
	// directory's cache. An extension with no cell is fused when the session's binary is a Piglet Binary (its process is
	// pig), and otherwise an exact standalone, which the host runs as an isolated process of its own.
	cell  string
	fused bool
	// run counts the terminals killed under the session so far.
	run int
}

// TestDeadTerminalEmitsSessionShutdownToExtensions kills the terminal of a real interactive pig with tmux kill-session and
// requires what Pi's extensions get there: session_shutdown with the reason "quit", awaited, before the exit, and exit 129.
//
// Pi 1.5.2, measured with the same extension (a 200 ms wait) after one turn, 4 runs per cell; handler reason / awaited
// write landed / exit code / ms from the kill to the exit:
//
//	                                        Pi, git repository          Pi, plain directory   PiG before the fix
//	tmux kill-session, pi under bash        quit 4/4, 4/4, 129, 212     none, 0, 18-20        none, 129, 4-13
//	tmux kill-pane                          quit 4/4, 4/4, 129, 211     none, 0, 14-19        none, 129, 4-13
//	pty master closed, pi session leader    quit 4/4, 4/4, 129, 213-258 none, 0, 11-61        none, 129, 37-45
//	pty master closed, pi under sh          quit 4/4, 4/4, 129, 211-253 none, 0, 14-53        none, 129, 38-43
//	kill -HUP on a live terminal            quit 4/4, 4/4, 0, 262-277                         quit, 0, 254-257
//
// Pi's order in the repository: stdin end, SIGHUP, session_shutdown, the awaited write, stdout EIO, exit 129
// (interactive-mode.ts registerSignalHandlers, shutdown fromSignal, emergencyTerminalExit). In a plain directory an idle
// Pi's event loop is empty at the hangup and it exits 0 before any handler; PiG exits 129 there and emits the event (D186).
// Pi awaits a 3 s handler in full when idle (8 of 8, exit 129 after 3.01-3.06 s), where PiG waits one second. While a
// turn is in flight Pi's next terminal write fails and ends it under the handler (started 8 of 8, awaited write 0 of 8,
// exit 129 after 23-28 ms); PiG's handler finishes there, which the "turn in flight" setup holds (D186), and
// TestDeadTerminalWithATurnInFlightWaitsOneSecondForTheShutdownHandler holds the bound of that wait.
// PiG's input reader sees the dead terminal before any signal, and its emergency exit used to skip the event and kill the
// extension processes (#25 of the copiglet filing: every captured session ended `terminal gone: EOF (hung up)`).
//
// A fixture's name says how the host runs the extension, and the command line of the extension's process has to agree with
// it. The Node rows are a factory alone and two factories in one Node process. The Go rows are one extension in its three
// realizations: the conventional factory of an unbuilt Piglet, which the host builds into a packed Go cell; the same source
// as an exact standalone, which is always an isolated process; and the factory fused into a Piglet Binary, where it runs
// in the pig process. Each runs under an interactive shell, which dies of the hangup as a user's does, and under a shell
// that ignores SIGHUP and records pig's exit status, idle and with a turn in flight (the faux provider's bash call that
// sleeps). The Python, Rust and Lisp rows are the factory of each SDK in its packed cell, idle under the shell that records the
// status: the event reaches every SDK through the same emission, so one setup shows that the SDK's handler is awaited.
// No process of the session, and no extension process, is left behind.
func TestDeadTerminalEmitsSessionShutdownToExtensions(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH:", err)
	}
	bin := buildBinary(t)
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name    string
		members int
		// cell and fused are the realization the name states (deadTerminalSession).
		cell  string
		fused bool
		args  func(dir string) []string
		// idleOnly runs the idle setup that records pig's exit status, and no other.
		idleOnly bool
	}{
		{name: "node factory", members: 1, cell: "node", args: func(dir string) []string {
			return []string{"-e", writeDeadTerminalFile(t, dir, "probe.mjs", deadTerminalProbeExtension)}
		}},
		// Two factories share one Node process (a packed cell); each gets the event.
		{name: "node packed pair", members: 2, cell: "node", args: func(dir string) []string {
			return []string{"-e", writeDeadTerminalFile(t, dir, "first.mjs", deadTerminalProbeExtension), "-e", writeDeadTerminalFile(t, dir, "second.mjs", deadTerminalProbeExtension)}
		}},
		{name: "go packed", members: 1, cell: "go", args: func(string) []string { return []string{"--piglet", deadTerminalPiglet(root)} }},
		{name: "go isolated", members: 1, args: func(dir string) []string {
			return []string{"-e", writeDeadTerminalGoStandalone(t, root, dir)}
		}},
		// The Piglet Binary registers the extension in the pig process itself.
		{name: "go fused", members: 1, fused: true, args: func(string) []string { return nil }},
		// The file is named after the identity the factory registers.
		{name: "python packed", members: 1, cell: "python", idleOnly: true, args: func(dir string) []string {
			return []string{"-e", writeDeadTerminalFile(t, dir, "deadterm_py.py", deadTerminalPythonExtension)}
		}},
		{name: "rust packed", members: 1, cell: "rust", idleOnly: true, args: func(dir string) []string {
			writeDeadTerminalFile(t, dir, "deadterm-rs/Cargo.toml", deadTerminalRustCargo)
			return []string{"-e", filepath.Dir(filepath.Dir(writeDeadTerminalFile(t, dir, "deadterm-rs/src/lib.rs", deadTerminalRustExtension)))}
		}},
		{name: "lisp packed", members: 1, cell: "lisp", idleOnly: true, args: func(dir string) []string {
			writeDeadTerminalFile(t, dir, "deadterm-lisp/deadterm-lisp.asd", deadTerminalLispSystem)
			return []string{"-e", filepath.Dir(writeDeadTerminalFile(t, dir, "deadterm-lisp/deadterm-lisp.lisp", deadTerminalLispExtension))}
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// One agent directory for the fixture's runs, so a fixture that the host builds is built once.
			home, cwd := deadTerminalHome(t)
			args := append(fixture.args(home), "--model", "test-faux/faux-1")
			bin := bin
			if fixture.fused {
				bin = buildDeadTerminalPigletBinary(t, bin, deadTerminalPiglet(root))
			}
			for _, setup := range []struct {
				name                  string
				recordStatus, working bool
			}{
				{"interactive shell", false, false},
				{"shell ignoring SIGHUP", true, false},
				{"turn in flight", true, true},
			} {
				if fixture.idleOnly && (!setup.recordStatus || setup.working) {
					continue
				}
				t.Run(setup.name, func(t *testing.T) {
					killDeadTerminals(t, deadTerminalSession{
						bin: bin, home: home, cwd: cwd, args: args, members: fixture.members, cell: fixture.cell, fused: fixture.fused,
						recordStatus: setup.recordStatus, working: setup.working, handlerWait: deadTerminalHandlerWait,
					})
				})
			}
		})
	}
}

// TestDeadTerminalWithATurnInFlightWaitsOneSecondForTheShutdownHandler kills the terminal while a turn is in flight, where
// Pi's next terminal write ends it under the handler (Pi 1.5.2, a 3 s handler: started 8 of 8, awaited write 0 of 8, exit
// 129 after 23-28 ms). PiG writes nothing to the dead terminal and gives the handler the second every dead-terminal exit
// gives it (D186; measured 1.00-1.02 s for a 3 s handler, 209-213 ms for a 200 ms handler): a handler slower than the
// second starts once and is cut, with the exit 129 at the bound and one exit.log line, and a 200 ms handler is awaited.
func TestDeadTerminalWithATurnInFlightWaitsOneSecondForTheShutdownHandler(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH:", err)
	}
	bin := buildBinary(t)
	home, cwd := deadTerminalHome(t)
	args := []string{"-e", writeDeadTerminalFile(t, home, "probe.mjs", deadTerminalProbeExtension), "--model", "test-faux/faux-1"}
	for _, handler := range []struct {
		name string
		wait time.Duration
	}{
		{"3 s handler is cut at the bound", 3 * time.Second},
		{"200 ms handler is awaited", 200 * time.Millisecond},
	} {
		t.Run(handler.name, func(t *testing.T) {
			killDeadTerminals(t, deadTerminalSession{
				bin: bin, home: home, cwd: cwd, args: args, members: 1, cell: "node",
				recordStatus: true, working: true, handlerWait: handler.wait,
			})
		})
	}
}

// deadTerminalHome returns a seeded agent home with the faux model and a trusted working directory.
func deadTerminalHome(t *testing.T) (home, cwd string) {
	t.Helper()
	home = t.TempDir()
	seedIntegrationSettings(t, home)
	if err := os.WriteFile(filepath.Join(home, "agent", "models.json"), []byte(uiViewModels), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd = t.TempDir()
	trustIntegrationProjectAt(t, home, cwd)
	return home, cwd
}

// killDeadTerminals kills deadTerminalRuns terminals under the session and fails for each ending that is not the expected one.
func killDeadTerminals(t *testing.T, session deadTerminalSession) {
	t.Helper()
	failures := 0
	var slowest time.Duration
	for run := range deadTerminalRuns {
		session.run = run
		took, problem := killTerminalUnderOnePig(t, session)
		slowest = max(slowest, took)
		if problem != "" {
			failures++
			t.Errorf("run %d: %s", run, problem)
		}
	}
	t.Logf("slowest exit after kill-session: %s", slowest)
	if failures > 0 {
		t.Errorf("%d of %d dead terminals did not give the extension its session_shutdown and exit 129", failures, deadTerminalRuns)
	}
}

func deadTerminalPiglet(root string) string {
	return filepath.Join(root, "test", "integration", "testdata", "dead-terminal", "piglet.yaml")
}

// buildDeadTerminalPigletBinary builds the Piglet Binary of the Go fixture with `pig piglet build --format binary`.
func buildDeadTerminalPigletBinary(t *testing.T, bin, piglet string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "pig-dead-terminal")
	cmd := exec.Command(bin, "piglet", "build", piglet, "--format", "binary", "--builder", "native", "--out", out)
	// The agent-directory variables the test process scopes would override PIG_HOME; empty them as the tmux panes do.
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(),
		"PIG_CODING_AGENT_DIR=", "PIG_CODING_AGENT_SESSION_DIR=", "PI_CODING_AGENT_DIR=", "PI_CODING_AGENT_SESSION_DIR=")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pig piglet build: %v\n%s", err, output)
	}
	return out
}

// writeDeadTerminalFile writes a fixture file under dir and returns its path.
func writeDeadTerminalFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeDeadTerminalGoStandalone writes the Go fixture as an exact standalone (a main package that runs the extension), which
// the host starts as an isolated process, and returns its directory, which carries the extension's name. The source is the
// factory's, so the two cannot differ.
func writeDeadTerminalGoStandalone(t *testing.T, root, dir string) string {
	t.Helper()
	factory, err := os.ReadFile(filepath.Join(filepath.Dir(deadTerminalPiglet(root)), "deadterm", "extension.go"))
	if err != nil {
		t.Fatal(err)
	}
	standalone := strings.NewReplacer("package deadterm\n", "package main\n", "func Extension() *sdk.Extension {", "func newExtension() *sdk.Extension {").Replace(string(factory))
	if !strings.Contains(standalone, "package main\n") || !strings.Contains(standalone, "func newExtension()") {
		t.Fatal("the Go fixture is no longer `package deadterm` with `func Extension() *sdk.Extension`")
	}
	standalone += "\nfunc main() {\n\tif err := newExtension().Run(); err != nil {\n\t\tpanic(err)\n\t}\n}\n"
	writeDeadTerminalFile(t, dir, "standalone/deadterm/go.mod", "module example.com/deadterm\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n")
	return filepath.Dir(writeDeadTerminalFile(t, dir, "standalone/deadterm/main.go", standalone))
}

// killTerminalUnderOnePig starts pig in a tmux pane under a shell, kills the session once every extension is ready (with working,
// once the turn's tool runs), and returns how long the pane's processes took to exit and what is wrong with the ending, or "".
func killTerminalUnderOnePig(t *testing.T, s deadTerminalSession) (took time.Duration, problem string) {
	t.Helper()
	// ai.TestFauxToolStartedMarker: the faux provider's sleeping bash call creates it in the working directory.
	toolStarted := filepath.Join(s.cwd, ".pig-faux-tool-started")
	if err := os.Remove(toolStarted); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	marker, status, exitLog := filepath.Join(scratch, "marker"), filepath.Join(scratch, "status"), filepath.Join(s.home, "agent", "exit.log")
	if err := os.Remove(exitLog); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	command := "cd '" + s.cwd + "' && env PIG_HOME='" + s.home + "' PIG_OFFLINE=1 PIG_TEST_FAUX=1 PIG_QUIET_STARTUP=1 PIG_DEAD_TERMINAL_MARKER='" + marker +
		"' PIG_DEAD_TERMINAL_WAIT_MS=" + strconv.FormatInt(s.handlerWait.Milliseconds(), 10) + " '" + s.bin + "' " + strings.Join(s.args, " ")
	session := "pig-dead-" + randID()
	if s.recordStatus {
		// The shell survives the hangup, so pig gets no SIGHUP from it and its exit status is readable.
		command = "trap '' HUP; " + command + "; echo $? > '" + status + "'"
		if out, err := tmuxCommand("new-session", "-d", "-s", session, "-x", "120", "-y", "34", command).CombinedOutput(); err != nil {
			t.Fatalf("tmux new-session: %v\n%s", err, out)
		}
	} else {
		if out, err := tmuxCommand("new-session", "-d", "-s", session, "-x", "120", "-y", "34", "bash --norc --noprofile -i").CombinedOutput(); err != nil {
			t.Fatalf("tmux new-session: %v\n%s", err, out)
		}
		for _, keys := range [][]string{{"-l", command}, {"Enter"}} {
			if out, err := tmuxCommand(append([]string{"send-keys", "-t", session}, keys...)...).CombinedOutput(); err != nil {
				t.Fatalf("tmux send-keys: %v\n%s", err, out)
			}
		}
	}
	pane, err := paneSessionID(session)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = killSession(t, session) }()

	var extensions []int
	// The first start of a fixture that the host builds (Go, Rust, Lisp) builds it.
	if !waitFor(4*time.Minute, func() bool {
		extensions = extensions[:0]
		data, _ := os.ReadFile(marker)
		for line := range strings.Lines(string(data)) {
			if pid, ok := strings.CutPrefix(strings.TrimSpace(line), "ready pid="); ok {
				n, _ := strconv.Atoi(pid)
				extensions = append(extensions, n)
			}
		}
		return len(extensions) == s.members
	}) {
		out, _ := tmuxCommand("capture-pane", "-p", "-t", session).Output()
		t.Fatalf("%d of %d extensions ready; pane:\n%s", len(extensions), s.members, out)
	}
	// The row's name states a realization: the process the host started for the extension has to be that one.
	for _, pid := range extensions {
		raw, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			t.Fatalf("ps of extension process %d: %v", pid, err)
		}
		line := strings.TrimSpace(string(raw))
		inPig := line == s.bin || strings.HasPrefix(line, s.bin+" ")
		switch {
		case s.fused && !inPig:
			t.Fatalf("extension process %d is %q, want the pig process itself (a fused extension)", pid, line)
		case s.cell != "" && !strings.Contains(line, "/cache/cells/"+s.cell+"/"):
			t.Fatalf("extension process %d is %q, want the runner of a packed %s cell", pid, line, s.cell)
		case !s.fused && s.cell == "" && inPig:
			t.Fatalf("extension process %d is %q, want an isolated process", pid, line)
		case !s.fused && s.cell == "" && s.run == 0:
			t.Logf("isolated extension process: %s", line)
		}
	}

	if s.working {
		for _, keys := range [][]string{{"-l", "Run: sleep for a while"}, {"Enter"}} {
			if out, err := tmuxCommand(append([]string{"send-keys", "-t", session}, keys...)...).CombinedOutput(); err != nil {
				t.Fatalf("tmux send-keys: %v\n%s", err, out)
			}
		}
		if !waitFor(startupReadyWait, func() bool { _, err := os.Stat(toolStarted); return err == nil }) {
			out, _ := tmuxCommand("capture-pane", "-p", "-t", session).Output()
			t.Fatalf("the turn's tool never started; pane:\n%s", out)
		}
	}

	start := time.Now()
	// hangUpSession fails the test if a process of the pane's session is still running 30 s after kill-session.
	if err := hangUpSession(t, session, pane, testenv.LiveSessionProcesses); err != nil {
		t.Fatal(err)
	}
	took = time.Since(start)
	for _, pid := range extensions {
		if !waitFor(5*time.Second, func() bool { return processExited(pid) }) {
			return took, "extension process " + strconv.Itoa(pid) + " outlived pig"
		}
	}

	// The handlers are awaited before the exit, so the marker is complete once pig is gone.
	data, _ := os.ReadFile(marker)
	if got := strings.Count(string(data), "session_shutdown reason=quit\n"); got != s.members {
		return took, "session_shutdown reached " + strconv.Itoa(got) + " of " + strconv.Itoa(s.members) + " extensions; marker: " + strconv.Quote(string(data))
	}
	settled := strings.Count(string(data), "session_shutdown settled reason=quit\n")
	if s.handlerWait > deadTerminalShutdownBound {
		// The handler outlasts the bound: the exit comes after the bound and before the handler's end.
		switch {
		case settled != 0:
			return took, "the exit waited for a handler slower than the bound; marker: " + strconv.Quote(string(data))
		case took < deadTerminalShutdownBound:
			return took, "the exit came after " + took.String() + ", before the handler had its " + deadTerminalShutdownBound.String()
		case took >= s.handlerWait:
			return took, "the exit came after " + took.String() + ", not at the bound of " + deadTerminalShutdownBound.String()
		}
	} else if settled != s.members {
		return took, "the exit did not wait for the handler; marker: " + strconv.Quote(string(data))
	}
	log, err := os.ReadFile(exitLog)
	if err != nil {
		return took, "no exit.log: " + err.Error()
	}
	lines := strings.Split(strings.TrimSuffix(string(log), "\n"), "\n")
	gone := len(lines) == 1 && strings.Contains(lines[0], " terminal gone: ") && strings.HasSuffix(lines[0], " (exit 129)")
	switch {
	case len(lines) != 1:
		return took, "exit.log is " + strings.Join(lines, " | ")
	case s.recordStatus && !gone:
		return took, "exit.log names no dead terminal: " + lines[0]
	case !gone && !strings.HasSuffix(lines[0], " received SIGHUP"):
		return took, "exit.log names no hangup cause: " + lines[0]
	}
	if s.recordStatus {
		raw, err := os.ReadFile(status)
		if err != nil {
			return took, "no exit status: " + err.Error()
		}
		if code := strings.TrimSpace(string(raw)); code != "129" {
			return took, "exit status " + code + ", want 129"
		}
	}
	return took, ""
}
