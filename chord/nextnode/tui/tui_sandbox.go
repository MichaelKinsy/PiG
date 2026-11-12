package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/nextnode/surfaces/client"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/node/tui/sandbox.ts. The terminal's sandbox (slice 11 §4, decision "Slice 11, P8"; fix round 2: a
// child process, as the owner decided after the worker thread was escaped through its shared realm): a Node child process.
//   - The operating system's side: its stdin, stdout and stderr are pipes to the launcher (never the terminal: no descriptor of
//     the child is a TTY), its environment empty, its directory a private one holding only the bootstrap; Node's permission model
//     with nothing allowed but reading the bootstrap (no other file read, no write, no network, no child processes, no workers,
//     no addons, no inspector, no WASI) and `node:sqlite` removed (`--no-experimental-sqlite`: SQLite opens files past the
//     permission model). An escape from the JavaScript layer reaches no terminal, no file, no socket, no launcher memory or token.
//   - The JavaScript side, a second layer: downloaded code (the base bundle, a CommonJS script; the plugins' registered scripts)
//     is only evaluated by the bootstrap (`vm.Script` with no dynamic import callback, so `import()` throws), with a `require`
//     answering the allowed builtins only (SandboxBuiltins); `process` is a substitute with SandboxProcess only. The bootstrap
//     keeps its own references to what it uses before any downloaded code runs.
//
// Messages go both ways as length-prefixed binary frames (pipe). Known gaps, each needing the JavaScript layer broken first
// unless noted: the child may signal processes of the same user (Node has no permission for signals; the launcher ignores
// SIGUSR1, which would open its inspector), read machine details (`os`), learn whether any path exists (`require.resolve` fails
// one way for a file that exists, another for one that does not: Opus E13), and allocate memory outside its heap (no limit
// without a shell's `ulimit`; only the child dies). Needs Node 26.5.1 or later.
//
// Forced Go difference (language mechanics: a Go program is not Node). Pi's launcher runs on Node and starts the sandbox with its own binary (`process.execPath`); the
// Go launcher starts the Node of SandboxOptions.Node (a Node 26.5.1 or later), and the bootstrap is Pi's text, vendored.

// maxBundle is the largest base bundle a sandbox runs: 8 MiB.
const maxBundle = 8 * 1024 * 1024

// SandboxBuiltins are the builtins code in the sandbox may `require`, each computation in memory without I/O, descriptors or
// machine details: bytes and text (`buffer`, `string_decoder`, `punycode`, `querystring`), events and streams in memory
// (`events`, `stream` and its submodules), formatting and type checks (`util`, `util/types`), URL and path strings (`url`,
// `path`), timers, assertions, hashing and randomness (`crypto`: no keys from files, no network) and compression in memory
// (`zlib`). Not `fs`, `tty`, `net`, `dgram`, `dns`, `http`, `https`, `http2`, `tls`, `child_process`, `cluster`,
// `worker_threads`, `sqlite`, `inspector`, `vm`, `v8`, `os`, `module`, `process`, `perf_hooks`, `wasi`, `repl`, `readline`,
// `trace_events` or anything else.
var SandboxBuiltins = []string{
	"assert", "assert/strict", "buffer", "crypto", "events", "path", "path/posix", "path/win32", "punycode", "querystring", "stream",
	"stream/consumers", "stream/promises", "stream/web", "string_decoder", "timers", "timers/promises", "url", "util", "util/types", "zlib",
}

// SandboxProcess are the properties of the sandbox's `process`: a frozen plain object in place of Node's (no event emitter, so no
// prototype shared with Node's internals leads back to the real one). Each harmless: which Node it is (`arch`, `platform`,
// `version`, `versions`, `release`), time (`nextTick`, `hrtime`, `uptime`), ending the sandbox (`exit`), the permission checks
// (`permission`), the allowed builtins (`getBuiltinModule`), fixed values (`env` empty, `argv`, `argv0`, `execArgv`, `execPath`,
// `title`, `cwd` `/`), `stdout` (to the terminal through the bridge) and `stderr` (dropped), and listener calls that keep nothing
// (`on`, `once`, `off`, `addListener`, `removeListener`, `removeAllListeners`, `emit`, `emitWarning`, `listenerCount`,
// `listeners`). Not `binding`, `_linkedBinding`, `dlopen`, `_rawDebug`, `kill`, `execve`, `chdir`, `abort`, `reallyExit`, `stdin`,
// `openStdin`, `report`, `pid`, `ppid`, `config`, the user and group calls, `loadEnvFile`, `moduleLoadList`, resource and handle
// listings or any other.
var SandboxProcess = []string{
	"addListener", "arch", "argv", "argv0", "cwd", "emit", "emitWarning", "env", "execArgv", "execPath", "exit", "getBuiltinModule",
	"hrtime", "listenerCount", "listeners", "nextTick", "off", "on", "once", "permission", "platform", "release", "removeAllListeners",
	"removeListener", "stderr", "stdout", "title", "uptime", "version", "versions",
}

// SandboxFlags are the child's Node flags for its script: the permission model allowing only reading that file, no SQLite, a
// 96 MiB heap. Exported for the tests of the operating system's layer alone.
func SandboxFlags(script string) []string {
	return []string{"--permission", "--allow-fs-read=" + script, "--no-experimental-sqlite", "--max-old-space-size=96", script}
}

// BootstrapFile is the bootstrap's file in the sandbox's directory: a script, not `-e`, which puts every builtin on `globalThis`.
const BootstrapFile = "bootstrap.cjs"

// maxUnread is the most bytes waiting for the sandbox to read them before it counts as stuck: 64 MiB.
const maxUnread = 64 * 1024 * 1024

// RequireSandboxNode fails unless version (`26.5.1`, a Node's `process.versions.node`) is 26.5.1 or later, the Node the
// sandbox's permissions were checked on.
func RequireSandboxNode(version string) error {
	var parts [3]int
	for i, part := range strings.SplitN(version, ".", 3) {
		//portlint:allow numbers the parts are the digits of `process.versions.node` split on dots (digits only; a failure leaves 0, as Number(part) || 0)
		parts[i], _ = strconv.Atoi(part)
	}
	major, minor, patch := parts[0], parts[1], parts[2]
	if major > 26 || (major == 26 && (minor > 5 || (minor == 5 && patch >= 1))) {
		return nil
	}
	return fmt.Errorf("the terminal client needs Node 26.5.1 or later for its sandbox (this is Node %s)", version)
}

// NodeVersion is the version of the Node binary at node (`node --version` without its `v`).
func NodeVersion(node string) (string, error) {
	output, err := exec.Command(node, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("the terminal client needs Node 26.5.1 or later for its sandbox: %w", err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(output)), "v"), nil
}

// CheckSandboxNode fails unless the Node at node is 26.5.1 or later.
func CheckSandboxNode(node string) error {
	version, err := NodeVersion(node)
	if err != nil {
		return err
	}
	return RequireSandboxNode(version)
}

// SandboxEnvironment is the child's environment: empty, but for LD_LIBRARY_PATH when the launcher has one: the launcher needed it
// to run this same binary (a Node built against libraries outside the system's paths), and the server cannot influence it.
func SandboxEnvironment() []string {
	if libraries, ok := os.LookupEnv("LD_LIBRARY_PATH"); ok {
		return []string{"LD_LIBRARY_PATH=" + libraries}
	}
	return []string{}
}

// bootstrap is the trusted bootstrap: Pi's text with its two interpolated values.
func bootstrap() (string, error) {
	builtins, err := jsstring.MarshalJSON(stringsAsAny(SandboxBuiltins))
	if err != nil {
		return "", err
	}
	text := strings.Replace(client.TuiBootstrapTemplate, "@MAX_PIPE_FRAME@", strconv.Itoa(MaxPipeFrame), 1)
	return strings.Replace(text, "@SANDBOX_BUILTINS@", string(builtins), 1), nil
}

func stringsAsAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

// Sandbox is a running sandbox: messages both ways, and its end.
type Sandbox struct {
	cmd       *exec.Cmd
	directory string
	stdin     io.WriteCloser
	onMessage func(SandboxMessage)
	onEnd     func(reason string)
	reader    *FrameReader
	exited    chan struct{}

	mu      sync.Mutex
	ready   *sync.Cond
	ended   bool
	queue   [][]byte
	queued  int
	closing bool
	// running is whether the child has sent anything: its bootstrap was read, so the file goes.
	running bool
}

// StartSandbox starts a sandbox of the Node at node running the base bundle source (a CommonJS script) with start on its
// bridge; onMessage gets what it sends, onEnd its end (an exit, a crash, its memory limit, a malformed frame), once. Both run
// on goroutines of the sandbox's: they must not block.
func StartSandbox(node, source string, start BridgeStart, onMessage func(SandboxMessage), onEnd func(reason string)) (*Sandbox, error) {
	if err := CheckSandboxNode(node); err != nil {
		return nil, err
	}
	//portlint:allow utf16units Pi compares Buffer.byteLength(source) (bytes)
	if len(source) > maxBundle {
		return nil, errors.New("terminal: the base bundle exceeds 8 MiB")
	}
	text, err := bootstrap()
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "chord-tui-")
	if err != nil {
		return nil, err
	}
	// Node resolves its entry before reading it; a symlinked ancestor would need a broader file grant.
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	directory = resolved
	script := filepath.Join(directory, BootstrapFile)
	if err := os.WriteFile(script, []byte(text), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	first, err := StartFrame(start, source)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	cmd := exec.Command(node, SandboxFlags(script)...) //nolint:gosec // G204: the Node binary the launcher checked (CheckSandboxNode) and the sandbox flags; the script is the launcher's own temporary file
	cmd.Env = SandboxEnvironment()
	cmd.Dir = directory
	// Dropped: the sandbox writes only through the bridge, and the terminal is the launcher's.
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	sandbox := &Sandbox{cmd: cmd, directory: directory, stdin: stdin, onMessage: onMessage, onEnd: onEnd, reader: NewFrameReader(), exited: make(chan struct{})}
	sandbox.ready = sync.NewCond(&sandbox.mu)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(directory)
		return nil, fmt.Errorf("the terminal client failed: %w", err)
	}
	//portlint:allow golifetime ends when the child's stdin is closed or its pipe fails; Terminate and the child's exit end it
	go sandbox.write()
	//portlint:allow golifetime ends with the child's stdout, which closes when it exits
	go sandbox.read(stdout)
	sandbox.send(first)
	return sandbox, nil
}

// Post sends message to the sandbox; a sandbox not reading what it was sent is ended.
func (s *Sandbox) Post(message LauncherMessage) {
	frame, err := LauncherFrame(message)
	if err != nil {
		return
	}
	s.send(frame)
}

// send queues frame for the child's stdin; past the unread bound the sandbox is ended.
func (s *Sandbox) send(frame []byte) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	if s.queued+len(frame) > maxUnread+MaxPipeFrame {
		s.mu.Unlock()
		s.fail("the terminal client stopped reading")
		return
	}
	s.queue = append(s.queue, frame)
	s.queued += len(frame)
	s.ready.Signal()
	s.mu.Unlock()
}

// write is the child's stdin: the queued frames, in order. A write after the child ended fails here; its end is reported by the
// reader.
func (s *Sandbox) write() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.closing {
			s.ready.Wait()
		}
		if s.closing {
			s.mu.Unlock()
			return
		}
		frame := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		_, err := s.stdin.Write(frame)
		s.mu.Lock()
		s.queued -= len(frame)
		s.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// read is the sandbox's output: its messages, each passed on; a malformed one ends the sandbox. At its end the child is reaped.
func (s *Sandbox) read(stdout io.Reader) {
	buffer := make([]byte, 64*1024)
	for {
		n, err := stdout.Read(buffer)
		if n > 0 {
			s.chunk(buffer[:n])
		}
		if err != nil {
			break
		}
	}
	_ = s.cmd.Wait()
	s.end("the terminal client exited")
	s.mu.Lock()
	s.closing = true
	s.ready.Broadcast()
	s.mu.Unlock()
	_ = os.RemoveAll(s.directory)
	close(s.exited)
}

func (s *Sandbox) chunk(chunk []byte) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	first := !s.running
	s.running = true
	s.mu.Unlock()
	if first {
		// Read once at the start; a launcher killed later leaves at most an empty directory.
		_ = os.Remove(filepath.Join(s.directory, BootstrapFile))
	}
	messages, err := s.reader.Push(chunk)
	if err != nil {
		// Pi's `#read`: the push threw, the chunk's messages (none) are not passed on and the sandbox fails.
		s.fail(err.Error())
		return
	}
	for _, message := range messages {
		s.mu.Lock()
		ended := s.ended
		s.mu.Unlock()
		if !ended {
			s.onMessage(message)
		}
	}
}

func (s *Sandbox) fail(reason string) {
	_ = s.cmd.Process.Kill()
	s.end(reason)
}

// end reports reason once.
func (s *Sandbox) end(reason string) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.mu.Unlock()
	s.onEnd(reason)
}

// Terminate ends the sandbox (its end is not reported) and waits for its exit.
func (s *Sandbox) Terminate() {
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
	_ = s.cmd.Process.Kill()
	<-s.exited
}
