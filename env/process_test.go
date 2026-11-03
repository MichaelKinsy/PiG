//go:build !windows

package env

// Ports Pi 9c9ce8547 packages/env/test/process.test.ts (describes "openProcess", "input and output", "ending" and
// "failures to start"), and from Pi 032656b6c the test "kills what the program started when it exits" and the Windows skip of
// "stops writing to stdin a descendant still holds once the program has exited".
//
// The terminal tests' helpers (collector, remoteCode, processExists) are Unix only, so these tests are too; the Windows
// behavior of the daemon is compiled by `go vet` for Windows and is not run here.
//
// The programs are Node scripts, as upstream's: they run the same on every system. Each test runs against PiG's daemon and
// against Pi's, once the pinned Pi has the `process` operation.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const processTestTimeout = 20 * time.Second

// processConnection is a connection to the daemon under test and a fresh working directory for its programs.
func processConnection(t *testing.T) (*Connection, string) {
	t.Helper()
	cwd, err := os.MkdirTemp("", "pi-env-process-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cwd) })
	connection := NewConnection(ConnectionOptions{Command: []string{terminalDaemon}})
	t.Cleanup(connection.Close)
	return connection, cwd
}

// nodeProgram is a Node script as the program.
func nodeProgram(t *testing.T, script string) []string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("the process tests run Node scripts: %v", err)
	}
	return []string{node, "-e", script}
}

// waitProcessExit waits for the program to end and returns what Exited reports.
func waitProcessExit(t *testing.T, program *RemoteProcess) (int, error) {
	t.Helper()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := program.Exited()
		done <- result{code, err}
	}()
	select {
	case r := <-done:
		return r.code, r.err
	case <-time.After(processTestTimeout):
		t.Fatal("the program did not end")
		return 0, nil
	}
}

// waitUntilGone waits up to five seconds for the process to disappear.
func waitUntilGone(pid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	return !processAlive(pid)
}

// echoProgram echoes each stdin chunk to stdout, and `err:` lines to stderr; it exits with code 7 at the end of stdin.
const echoProgram = `
process.stdin.on("data", (chunk) => {
	const text = chunk.toString("latin1");
	if (text.startsWith("err:")) process.stderr.write(text.slice(4));
	else process.stdout.write(chunk);
});
process.stdin.on("end", () => process.exit(7));
`

func openProcess(t *testing.T, connection *Connection, options ProcessOptions) *RemoteProcess {
	t.Helper()
	program, err := OpenProcess(background, connection, options)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:82 "pipes stdin to the program and its stdout and stderr back separately"
// mutation-checked: sending the program's stderr to OnStdout in OpenProcess fails it.
func TestOpenProcessPipesStdinToTheProgramAndItsStdoutAndStderrBackSeparately(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		stdout, stderr := newCollector(), newCollector()
		program := openProcess(t, connection, ProcessOptions{Cwd: cwd, Argv: nodeProgram(t, echoProgram), OnStdout: stdout.onData, OnStderr: stderr.onData})
		if program.Pid <= 0 {
			t.Fatalf("pid = %d", program.Pid)
		}
		program.Write("{\"jsonrpc\":\"2.0\",\"id\":1}\n")
		stdout.waitFor(t, "{\"jsonrpc\":\"2.0\",\"id\":1}\n")
		program.Write("err:diagnostic\n")
		stderr.waitFor(t, "diagnostic\n")
		if got := string(stdout.bytes()); got != "{\"jsonrpc\":\"2.0\",\"id\":1}\n" {
			t.Fatalf("stdout = %q", got)
		}
		program.CloseStdin()
		if code, err := waitProcessExit(t, program); code != 7 || err != nil {
			t.Fatalf("exit = %d, %v", code, err)
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:102 "passes bytes through unchanged: no echo, no newline translation, no decoding"
func TestOpenProcessPassesBytesThroughUnchangedNoEchoNoNewlineTranslationNoDecoding(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		stdout := newCollector()
		program := openProcess(t, connection, ProcessOptions{Cwd: cwd, Argv: nodeProgram(t, echoProgram), OnStdout: stdout.onData})
		program.Write([]byte{0x61, 0x0a, 0xff, 0x0d, 0x0a, 0x62})
		program.CloseStdin()
		if code, err := waitProcessExit(t, program); code != 7 || err != nil {
			t.Fatalf("exit = %d, %v", code, err)
		}
		if got := stdout.bytes(); !bytes.Equal(got, []byte{0x61, 0x0a, 0xff, 0x0d, 0x0a, 0x62}) {
			t.Fatalf("stdout = %v", got)
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:116 "passes the working directory and environment"
func TestOpenProcessPassesTheWorkingDirectoryAndEnvironment(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		stdout := newCollector()
		program := openProcess(t, connection, ProcessOptions{
			Cwd:      cwd,
			Argv:     nodeProgram(t, "process.stdout.write(JSON.stringify([process.cwd(), process.env.PI_ENV_PROCESS_TEST]))"),
			Env:      map[string]string{"PI_ENV_PROCESS_TEST": "set"},
			OnStdout: stdout.onData,
		})
		if code, err := waitProcessExit(t, program); code != 0 || err != nil {
			t.Fatalf("exit = %d, %v", code, err)
		}
		output := string(stdout.bytes())
		if !strings.Contains(strings.ToLower(output), "pi-env-process-") || !strings.HasSuffix(output, `,"set"]`) {
			t.Fatalf("output = %s, want the working directory and PI_ENV_PROCESS_TEST=set", output)
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:133 "keeps input in order across many writes and writes larger than one frame chunk"
// mutation-checked: taking the newest chunk first in inputQueue.next reorders the input, changes the hash and fails it.
func TestOpenProcessKeepsInputInOrderAcrossManyWritesAndWritesLargerThanOneFrameChunk(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		stdout := newCollector()
		program := openProcess(t, connection, ProcessOptions{
			Cwd: cwd,
			// Reports the length and hash of everything it read once stdin ends.
			Argv: nodeProgram(t, `
				const hash = require("node:crypto").createHash("sha256");
				let length = 0;
				process.stdin.on("data", (chunk) => { hash.update(chunk); length += chunk.length; });
				process.stdin.on("end", () => process.stdout.write(length + " " + hash.digest("hex")));
			`),
			OnStdout: stdout.onData,
		})
		expected := sha256.New()
		length := 0
		for index := range 4000 {
			piece := []byte(fmt.Sprintf("%09d\n", index))
			expected.Write(piece)
			length += len(piece)
			program.Write(piece)
		}
		big := make([]byte, 2_500_003)
		for index := range big {
			big[index] = byte(index * 7919 % 251)
		}
		expected.Write(big)
		length += len(big)
		program.Write(big)
		program.CloseStdin()
		if code, err := waitProcessExit(t, program); code != 0 || err != nil {
			t.Fatalf("exit = %d, %v", code, err)
		}
		if want := fmt.Sprintf("%d %s", length, hex.EncodeToString(expected.Sum(nil))); string(stdout.bytes()) != want {
			t.Fatalf("stdout = %q, want %q", stdout.bytes(), want)
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:165 "delivers large output completely"
func TestOpenProcessDeliversLargeOutputCompletely(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		stdout := newCollector()
		program := openProcess(t, connection, ProcessOptions{Cwd: cwd, Argv: nodeProgram(t, `process.stdout.write(Buffer.alloc(12 * 1024 * 1024, 120))`), OnStdout: stdout.onData})
		if code, err := waitProcessExit(t, program); code != 0 || err != nil {
			t.Fatalf("exit = %d, %v", code, err)
		}
		output := stdout.bytes()
		if len(output) != 12*1024*1024 || len(bytes.Trim(output, "x")) != 0 {
			t.Fatalf("output has %d bytes, %d of them not x", len(output), len(bytes.Trim(output, "x")))
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:179 "refuses input after stdin was closed while the program keeps running"
// mutation-checked: not closing the input queue once the writer ended in runProcess makes every later write succeed and fails it.
func TestProcessWriteRefusesInputAfterStdinWasClosedWhileTheProgramKeepsRunning(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		started := make(chan struct{})
		var (
			once   sync.Once
			target uint32
		)
		connection.Start(background, "process", Json{"cwd": cwd, "argv": nodeProgram(t, "process.stdin.resume(); setTimeout(() => {}, 30000)")}, RequestOptions{
			OnStart: func(id uint32, _ int) { target = id },
			OnEvent: func(event Json, _ []byte) {
				if event["kind"] == "started" {
					once.Do(func() { close(started) })
				}
			},
		})
		select {
		case <-started:
		case <-time.After(processTestTimeout):
			t.Fatal("the program never started")
		}
		if _, err := connection.Request(background, "processWrite", Json{"target": target}, RequestOptions{Payload: []byte{1}}); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.Request(background, "processCloseStdin", Json{"target": target}, RequestOptions{}); err != nil {
			t.Fatal(err)
		}
		// The writer closes stdin after the input queued before it; from then on input is refused.
		var refused error
		for tries := 0; refused == nil && tries < 100; tries++ {
			if _, err := connection.Request(background, "processWrite", Json{"target": target}, RequestOptions{Payload: []byte{2}}); err != nil {
				refused = err
			} else {
				time.Sleep(10 * time.Millisecond)
			}
		}
		if remoteCode(refused) != "EBADF" {
			t.Fatalf("refused = %v, want EBADF", refused)
		}
		connection.Close()
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:213 "refuses input for programs that do not run"
func TestProcessWriteRefusesInputForProgramsThatDoNotRun(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, _ := processConnection(t)
		_, err := connection.Request(background, "processWrite", Json{"target": 999}, RequestOptions{})
		if remoteCode(err) != "EBADF" {
			t.Fatalf("processWrite error = %v", err)
		}
		_, err = connection.Request(background, "processCloseStdin", Json{"target": 999}, RequestOptions{})
		if remoteCode(err) != "EBADF" {
			t.Fatalf("processCloseStdin error = %v", err)
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:221 "reports the exit code of a program that exits on its own"
func TestOpenProcessReportsTheExitCodeOfAProgramThatExitsOnItsOwn(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		program := openProcess(t, connection, ProcessOptions{Cwd: cwd, Argv: nodeProgram(t, "process.exit(3)"), OnStdout: func([]byte) {}})
		if code, err := waitProcessExit(t, program); code != 3 || err != nil {
			t.Fatalf("exit = %d, %v", code, err)
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:227 "kills the program and what it started on close"
// mutation-checked: making RemoteProcess.Close a no-op leaves the program running and fails it.
func TestOpenProcessKillsTheProgramAndWhatItStartedOnClose(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		stdout := newCollector()
		program := openProcess(t, connection, ProcessOptions{
			Cwd: cwd,
			// A wrapper that starts the real server, as npx and uvx do.
			Argv: nodeProgram(t, `
				const child = require("node:child_process").spawn(process.execPath, ["-e", "setTimeout(() => {}, 30000)"], { stdio: "ignore" });
				process.stdout.write(child.pid + "\n");
				setTimeout(() => {}, 30000);
			`),
			OnStdout: stdout.onData,
		})
		stdout.waitFor(t, "\n")
		child := must(strconv.Atoi(strings.TrimSpace(string(stdout.bytes()))))
		if !processAlive(child) {
			t.Fatalf("the server %d does not run", child)
		}
		program.Close()
		if _, err := waitProcessExit(t, program); remoteCode(err) != "aborted" {
			t.Fatalf("Exited error = %v, want aborted", err)
		}
		if !waitUntilGone(program.Pid) {
			t.Fatalf("the program %d still runs", program.Pid)
		}
		if !waitUntilGone(child) {
			t.Fatalf("the server %d still runs", child)
		}
	})
}

// Pi 032656b6c packages/env/test/process.test.ts:249 "kills what the program started when it exits"
// mutation-checked: skipping the group kill after the program exited (programGroup.end in program.waitAndEnd) leaves the server running and fails it.
func TestOpenProcessKillsWhatTheProgramStartedWhenItExits(t *testing.T) {
	eachDaemonWith(t, skipWithoutPinnedProcessEnding, func(t *testing.T) {
		connection, cwd := processConnection(t)
		stdout := newCollector()
		program := openProcess(t, connection, ProcessOptions{
			Cwd: cwd,
			// A wrapper that exits and leaves the server it started running, as npx can once stdin closes. (Upstream detaches the
			// server on Windows, so that libuv's own job object does not take it down with the wrapper; these tests do not run there.)
			Argv: nodeProgram(t, `
				const child = require("node:child_process").spawn(process.execPath, ["-e", "setTimeout(() => {}, 30000)"], { stdio: "ignore" });
				process.stdout.write(child.pid + "\n", () => process.exit(0));
			`),
			OnStdout: stdout.onData,
		})
		if code, err := waitProcessExit(t, program); code != 0 || err != nil {
			t.Fatalf("exit = %d, %v", code, err)
		}
		child, err := strconv.Atoi(strings.TrimSpace(string(stdout.bytes())))
		if err != nil {
			t.Fatalf("the program reported %q: %v", stdout.bytes(), err)
		}
		if !waitUntilGone(child) {
			t.Fatalf("the server %d still runs after the program exited", child)
		}
	})
}

// Pi 032656b6c packages/env/test/process.test.ts:268 "stops writing to stdin a descendant still holds once the program has exited"
// Upstream skips it on Windows, where a descendant dies with the program's job object; these tests do not run there. On Unix
// the descendant leaves the process group.
// mutation-checked: dropping input.close and program.closePipes from the end of runProcess leaves the writer pushing the input into the stdin the descendant holds, so the program never ends, and fails it.
func TestOpenProcessStopsWritingToStdinADescendantStillHoldsOnceTheProgramHasExited(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		count := filepath.Join(cwd, "count.txt")
		// The descendant inherits stdin and reads it only after the program exited, until its end.
		descendant := `
			setTimeout(() => {
				let length = 0;
				process.stdin.on("data", (chunk) => { length += chunk.length; });
				process.stdin.on("end", () => { require("node:fs").writeFileSync(` + strconv.Quote(count) + `, String(length)); process.exit(0); });
			}, 1500);
			setTimeout(() => process.exit(1), 15000);
		`
		program := openProcess(t, connection, ProcessOptions{
			Cwd: cwd,
			Argv: nodeProgram(t, `
				require("node:child_process").spawn(process.execPath, ["-e", `+strconv.Quote(descendant)+`], { stdio: ["inherit", "ignore", "ignore"], detached: true }).unref();
				setTimeout(() => process.exit(0), 300);
			`),
			OnStdout: func([]byte) {},
		})
		// Far more than the pipe holds: the writer blocks until it is stopped.
		program.Write(make([]byte, 4*1024*1024))
		if code, err := waitProcessExit(t, program); code != 0 || err != nil {
			t.Fatalf("exit = %d, %v", code, err)
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(count); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		text, err := os.ReadFile(count)
		if err != nil {
			t.Fatalf("the descendant never reported what it read: %v", err)
		}
		if read := must(strconv.Atoi(string(text))); read >= 4*1024*1024 {
			t.Fatalf("the descendant read %d bytes of the 4 MiB written after the program exited", read)
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:305 "kills the program when the connection closes"
func TestOpenProcessKillsTheProgramWhenTheConnectionCloses(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		program := openProcess(t, connection, ProcessOptions{Cwd: cwd, Argv: nodeProgram(t, "setTimeout(() => {}, 30000)"), OnStdout: func([]byte) {}})
		connection.Close()
		_, err := waitProcessExit(t, program)
		if _, ok := errors.AsType[*RemoteError](err); !ok {
			t.Fatalf("Exited error = %v, want a RemoteError", err)
		}
		if !waitUntilGone(program.Pid) {
			t.Fatalf("the program %d still runs", program.Pid)
		}
	})
}

// startProcess opens a trivial Node program with the given options changed, in a connection and directory of its own.
func startProcess(t *testing.T, change func(options *ProcessOptions)) (*RemoteProcess, error) {
	t.Helper()
	connection, cwd := processConnection(t)
	options := ProcessOptions{Cwd: cwd, Argv: nodeProgram(t, ""), OnStdout: func([]byte) {}}
	change(&options)
	return OpenProcess(background, connection, options)
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:324 "fails in a missing directory"
func TestOpenProcessFailsInAMissingDirectory(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		_, err := startProcess(t, func(options *ProcessOptions) { options.Cwd = filepath.Join(options.Cwd, "missing") })
		if remoteCode(err) != "spawn_error" {
			t.Fatalf("error = %v, want spawn_error", err)
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:329 "fails for a program that does not exist"
func TestOpenProcessFailsForAProgramThatDoesNotExist(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		_, err := startProcess(t, func(options *ProcessOptions) { options.Argv = []string{"pi-env-no-such-program"} })
		if remoteCode(err) != "spawn_error" {
			t.Fatalf("error = %v, want spawn_error", err)
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:333 "rejects an empty argv"
func TestOpenProcessRejectsAnEmptyArgv(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		_, err := startProcess(t, func(options *ProcessOptions) { options.Argv = []string{} })
		if remoteCode(err) != "EINVAL" {
			t.Fatalf("error = %v, want EINVAL", err)
		}
	})
}

// Pi 9c9ce8547 packages/env/test/process.test.ts:337 "reports daemons released before the operation as not supported"
func TestOpenProcessReportsDaemonsReleasedBeforeTheOperationAsNotSupported(t *testing.T) {
	cwd := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	connection := NewConnection(ConnectionOptions{Command: []string{self, "old-daemon"}})
	t.Cleanup(connection.Close)
	_, err = OpenProcess(background, connection, ProcessOptions{Cwd: cwd, Argv: nodeProgram(t, ""), OnStdout: func([]byte) {}})
	if remoteCode(err) != "not_supported" {
		t.Fatalf("error = %v, want not_supported", err)
	}
}

// Go addition: the daemon reports the `process` feature in hello, as upstream's main.rs FEATURES lists it.
func TestHelloReportsTheProcessFeature(t *testing.T) {
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)
	info, err := connection.Info(background)
	if err != nil || !slices.Contains(info.Features, "process") {
		t.Fatalf("hello features = %v, %v; want process", info.Features, err)
	}
}
