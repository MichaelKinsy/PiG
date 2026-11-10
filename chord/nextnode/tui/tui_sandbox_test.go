package tui

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Tests of the terminal's sandbox (slice 11 §4, decision "Slice 11, P8"; fix rounds 1 to 3: a child process, every way to modules
// and descriptors closed). The operating system's layer alone: no terminal descriptor, no `/dev/tty`, no file, no SQLite, no
// network, no child process, worker, addon or inspector, no environment (signals, machine details and whether a path exists are
// its known gaps). The whole sandbox, from the app's terminal code and from a plugin: no builtin past the allowlist (shared
// prototypes changed first included), no getter on a prototype of Node's process, no `import()`, no Node `process` (the second
// is in nextnode/app: it needs a kit app). Run under Node 26.5.1 or later; skipped without. Ports
// packages/chord/test/node/tui/sandbox.test.ts.

func TestTheSandboxStartsWithASymlinkedTemporaryDirectoryWithoutExpandingFilePermissions(t *testing.T) {
	node := tuiNode(t)
	root := t.TempDir()
	target := filepath.Join(root, "target")
	linked := filepath.Join(root, "linked")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, target, linked)
	t.Setenv("TMPDIR", linked)
	t.Setenv("TMP", linked)
	t.Setenv("TEMP", linked)
	messages := make(chan SandboxMessage, 4)
	ended := make(chan string, 1)
	sandbox, err := StartSandbox(node, `globalThis.chordBridge.send({ kind: "exit", code: 0 });`,
		BridgeStart{Base: "test", Columns: 80, Rows: 24, Colors: "truecolor", Mode: "dark", Locales: []string{"en"}},
		func(m SandboxMessage) { messages <- m }, func(reason string) { ended <- reason })
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Terminate()
	select {
	case m := <-messages:
		if m.Kind != SandboxExit {
			t.Fatalf("first message = %+v, want exit", m)
		}
	case reason := <-ended:
		t.Fatalf("the sandbox ended before its exit message: %s", reason)
	case <-time.After(30 * time.Second):
		t.Fatal("no message from the sandbox")
	}
	// The grant stays the one script: its directory is the resolved path, with no symlink in it, and not the linked one.
	resolved, err := filepath.EvalSymlinks(sandbox.directory)
	if err != nil || resolved != sandbox.directory {
		t.Fatalf("directory %q is not resolved (%q, %v)", sandbox.directory, resolved, err)
	}
	if strings.HasPrefix(sandbox.directory, linked) {
		t.Fatalf("directory %q is under the symlink %q", sandbox.directory, linked)
	}
}

func TestTheSandboxAsAProcessReachesNoTerminalFileSQLiteNetworkOrEnvironmentPastAnyJavaScriptOpusE1E2(t *testing.T) {
	node := tuiNode(t)
	// The probe runs as the sandbox's script (os-layer.ts): the sandbox's flags, environment, pipes and private directory, but the
	// probe instead of the bootstrap, so it has every builtin and Node's own `process`, as code that got past the JavaScript layer.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	probe, err := os.ReadFile(filepath.Join("testdata", "os_layer_probe.cjs"))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	script := filepath.Join(directory, BootstrapFile)
	if err := os.WriteFile(script, probe, 0o600); err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	cmd := exec.Command(node, append(SandboxFlags(script), port)...)
	cmd.Env = SandboxEnvironment()
	cmd.Dir = directory
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, output)
	}
	var outcomes map[string]string
	if err := json.Unmarshal(output[strings.IndexByte(string(output), '{'):], &outcomes); err != nil {
		t.Fatalf("outcomes: %v\n%s", err, output)
	}
	if fetch := outcomes["fetch"]; fetch == "ok" {
		t.Fatalf("fetch reached the network")
	}
	delete(outcomes, "fetch")
	want := map[string]string{
		"a terminal descriptor": "none",
		"open /dev/tty":         "ERR_ACCESS_DENIED",
		"read a file":           "ERR_ACCESS_DENIED",
		"list its directory":    "ERR_ACCESS_DENIED",
		"write a file":          "ERR_ACCESS_DENIED",
		"node:sqlite":           "absent",
		"require sqlite":        "ERR_UNKNOWN_BUILTIN_MODULE",
		"net.connect":           "ERR_ACCESS_DENIED",
		"a child process":       "ERR_ACCESS_DENIED",
		"a worker":              "ERR_ACCESS_DENIED",
		"an addon":              "ERR_DLOPEN_DISABLED",
		"the inspector":         "ERR_ACCESS_DENIED",
		"an environment":        "empty",
		// Known gaps: Node has no permission for signals, and the machine's details need none.
		"signal the launcher":   "ok",
		"os details":            "ok",
		"whether a path exists": "ok",
	}
	if !reflect.DeepEqual(outcomes, want) {
		t.Fatalf("outcomes = %v, want %v", outcomes, want)
	}
}

// sandboxRun starts a sandbox running script and collects what it sends (kind and text) until it ends.
func sandboxRun(t *testing.T, node, script string) [][2]string {
	t.Helper()
	var mu sync.Mutex
	var lines [][2]string
	ended := make(chan string, 1)
	sandbox, err := StartSandbox(node, script,
		BridgeStart{Base: "test", Columns: 80, Rows: 24, Colors: "truecolor", Mode: "dark", Locales: []string{"en"}},
		func(message SandboxMessage) {
			mu.Lock()
			lines = append(lines, [2]string{string(message.Kind), message.Data})
			mu.Unlock()
			if message.Kind == "exit" {
				select {
				case ended <- "exit":
				default:
				}
			}
		},
		func(reason string) {
			select {
			case ended <- reason:
			default:
			}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(30 * time.Second):
		t.Fatal("the sandbox did not end")
	}
	sandbox.Terminate()
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(lines)
}

func TestTheSandboxGivesRawDownloadedCodeNoBuiltinAndNoImportWhateverItPutsOnObjectPrototype(t *testing.T) {
	node := tuiNode(t)
	script := strings.Join([]string{
		"const bridge = globalThis.chordBridge;",
		`try { require("node:tty"); bridge.send({ kind: "write", data: "require ok" }); } catch (error) { bridge.send({ kind: "write", data: error.message }); }`,
		// Opus: an option on Object.prototype was read by the bridge's evaluation.
		`Object.prototype.importModuleDynamically = () => process.getBuiltinModule("node:events");`,
		`globalThis.report = (outcome) => bridge.send({ kind: "write", data: "evaluated import " + outcome });`,
		`bridge.evaluate("import('node:fs').then(() => 'ok', (error) => error.code).then(report)", "probe.js");`,
		`import("node:fs").then(() => "ok", (error) => error.code).then((outcome) => {`,
		`	bridge.send({ kind: "write", data: "import " + outcome });`,
		`	bridge.send({ kind: "exit", code: 0 });`,
		"});",
	}, "\n")
	got := sandboxRun(t, node, script)
	want := [][2]string{
		{"write", "node:tty: unavailable in the terminal's sandbox"},
		{"write", "evaluated import ERR_VM_DYNAMIC_IMPORT_CALLBACK_MISSING"},
		{"write", "import ERR_VM_DYNAMIC_IMPORT_CALLBACK_MISSING"},
		{"exit", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %q, want %q", got, want)
	}
}

func TestTheSandboxFlagsAndEnvironmentAreTheOperatingSystemLayerPi(t *testing.T) {
	flags := SandboxFlags("/tmp/x/bootstrap.cjs")
	want := []string{"--permission", "--allow-fs-read=/tmp/x/bootstrap.cjs", "--no-experimental-sqlite", "--max-old-space-size=96", "/tmp/x/bootstrap.cjs"}
	if !reflect.DeepEqual(flags, want) {
		t.Fatalf("flags = %q, want %q", flags, want)
	}
	t.Setenv("LD_LIBRARY_PATH", "/opt/lib")
	if got := SandboxEnvironment(); !reflect.DeepEqual(got, []string{"LD_LIBRARY_PATH=/opt/lib"}) {
		t.Fatalf("environment = %q", got)
	}
	_ = os.Unsetenv("LD_LIBRARY_PATH")
	if got := SandboxEnvironment(); len(got) != 0 {
		t.Fatalf("environment = %q, want empty", got)
	}
}

// Pi's sandbox `#read` takes `messages = reader.push(chunk)` whole: a malformed frame throws, the sandbox fails and not one message of
// that chunk reaches `onMessage`, the valid frames before the bad one included.
func TestASandboxChunkWithAMalformedFrameDeliversNoMessageAndEndsTheSandbox(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep command")
	}
	cmd := exec.Command(sleep, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var messages []SandboxMessage
	var reasons []string
	sandbox := &Sandbox{cmd: cmd, directory: t.TempDir(), reader: NewFrameReader(),
		onMessage: func(m SandboxMessage) { mu.Lock(); messages = append(messages, m); mu.Unlock() },
		onEnd:     func(reason string) { mu.Lock(); reasons = append(reasons, reason); mu.Unlock() },
	}
	sandbox.ready = sync.NewCond(&sandbox.mu)
	valid := sandboxFrame(t, map[string]any{"kind": "write"}, []byte("one"))
	sandbox.chunk(append(append(append([]byte(nil), valid...), valid...), 0, 0, 0, 0))
	_ = cmd.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 0 {
		t.Fatalf("%d messages reached onMessage, want none of a chunk with a malformed frame", len(messages))
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "malformed") {
		t.Fatalf("end reasons = %q, want one, the malformed frame", reasons)
	}
}
