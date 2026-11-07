package extensionconformance

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/earlydispatch"
)

// A host can shut an extension down after the extension registered and before the host marked it ready: a reload or a quit that ends a load in flight. Pi has no failing path for it: an extension that has not finished loading is not on the runner, and a session shutdown never fails an extension (runner.ts emitSessionShutdownEvent). The Node runtime, Pi's own, returns. Every SDK ends the run as it does after ready: it answers the listener dispatches it read before the shutdown, closes its end of the connection and exits with status 0 and nothing on stderr.
//
// The host here is a script on the wire. It answers the factory's host calls, reads the register frame, writes the shutdown with no ready and keeps its own end open, so the only thing that can end the connection is the extension acting on the frame. The extension's outcome is its exit status and stderr, or Run's return for the Go SDK in this process. The shutdown arrives at four points:
//
//   - after-register: after the register frame, proven alive by a ping answered first;
//   - dispatches-before-shutdown: one write carries two events.dispatch requests and the shutdown, so each request is answered exactly once under its own ID with no error, and the listener's own value is there: it makes no host call (PureListenerEnv) and writes the payload's tag to a file named by its seq, so an unknown-handler reply, a refusal, a cancellation or a dropped dispatch fails the row;
//   - during-subscription: while the factory's events.on call waits for its result, which the host answers right after the shutdown: the call returns, the extension registers, and then ends with nothing more on the wire;
//   - during-factory-call: the same while the factory's own events.emit call waits (FactoryEmitEnv), a call that goes over the connection the factory dialled and the run later takes.
//
// After the shutdown every SDK sends nothing further, except the register frame when the shutdown preceded it: the extension that began its run always registers, as the Node runtime does.
//
// Placements: every SDK as its own process (isolated), the Go SDK in this process over a pipe (fused, which has no factory connection to dial), and every SDK as a member of a two-member packed cell, whose runner exits non-zero when any member's run failed. The second member of a packed cell is marked ready before the first is shut down, and is served and ended normally after the first ended, so the first member's end does not stop the cell.

type shutdownPeer struct {
	conn net.Conn
	// end reports how the extension's run ended.
	end func(t *testing.T) processEnd
}

// shutdownCase is one point at which the host's shutdown arrives, run against an extension that has not received ready.
type shutdownCase struct {
	name string
	// factoryEmit makes the factory emit, so it makes a host call of its own besides events.on.
	factoryEmit bool
	// pureListener makes the listener write a file instead of echoing over a host call; after then reads what it wrote.
	pureListener bool
	run          func(t *testing.T, conn net.Conn, node bool)
	after        func(t *testing.T, listenerDir string)
}

var shutdownCases = []shutdownCase{
	{name: "after-register", run: func(t *testing.T, conn net.Conn, node bool) {
		readUntilRegister(t, conn, node)
		requireShutdownEndsTheConnection(t, conn)
	}},
	{name: "dispatches-before-shutdown", pureListener: true, run: func(t *testing.T, conn net.Conn, node bool) {
		handlerID := readUntilRegister(t, conn, node)
		frames := append(earlyDispatchFrames(t, handlerID, node), subprocess.Envelope{Type: subprocess.MsgShutdown, Shutdown: &subprocess.ShutdownPayload{Reason: "reload"}})
		writeSDKBurst(t, conn, frames...)
		responses := map[string][]subprocess.Envelope{}
		for {
			env, err := tryReadSDKEnvelope(conn)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("read after the shutdown: %v, want the extension's end of the connection (io.EOF)", err)
				}
				break
			}
			switch env.Type {
			case subprocess.MsgResponse:
				responses[env.ID] = append(responses[env.ID], env)
			case subprocess.MsgCall:
				t.Fatalf("host call %+v after the shutdown: the pure listener makes none", env.Call)
			}
		}
		// A listener that ran answers without an error, so an unknown-handler reply, a refusal or a cancellation fails here.
		for _, id := range []string{"d1", "d2"} {
			if len(responses[id]) != 1 {
				t.Errorf("answers under %s = %d, want exactly 1 (all: %v)", id, len(responses[id]), responses)
				continue
			}
			if got := responses[id][0].Response; got == nil || got.Error != nil {
				t.Errorf("answer under %s = %+v, want success", id, got)
			}
		}
		if len(responses) != 2 {
			t.Errorf("answers = %v, want only d1 and d2", responses)
		}
	}, after: func(t *testing.T, listenerDir string) {
		// The value the listeners produced: each dispatch's own tag in the file its own seq names.
		for seq, want := range map[string]string{"1": "first", "2": "second"} {
			got, err := os.ReadFile(filepath.Join(listenerDir, seq))
			if err != nil || string(got) != want {
				t.Errorf("listener file %s = %q (%v), want %q", seq, got, err, want)
			}
		}
	}},
	{name: "during-subscription", run: func(t *testing.T, conn net.Conn, node bool) {
		readUntilRegisterShuttingDown(t, conn, node, "events.on")
		requireEndOfConnection(t, conn)
	}},
	{name: "during-factory-call", factoryEmit: true, run: func(t *testing.T, conn net.Conn, node bool) {
		readUntilRegisterShuttingDown(t, conn, node, "events.emit")
		requireEndOfConnection(t, conn)
	}},
}

func TestShutdownBeforeReadyEndsTheRunCleanly(t *testing.T) {
	if testing.Short() {
		t.Skip("starts SDK subprocesses")
	}
	root := findModuleRoot(t)
	data := filepath.Join(root, "test", "extension-conformance", "testdata")
	type target struct {
		name string
		// start returns the extension's connection and how its run ended.
		start func(t *testing.T) shutdownPeer
		node  bool
		// noFactoryConnection is set for a placement whose factory has no host connection to dial.
		noFactoryConnection bool
	}
	isolated := func(command func(t *testing.T) func(string) *exec.Cmd, node bool) func(t *testing.T) shutdownPeer {
		return func(t *testing.T) shutdownPeer {
			conn, process := startSDKProcess(t, command(t), node)
			return shutdownPeer{conn: conn, end: process.end}
		}
	}
	targets := []target{
		{name: "go", start: isolated(func(t *testing.T) func(string) *exec.Cmd {
			bin := buildEarlyDispatchGo(t)
			return func(string) *exec.Cmd { return exec.Command(bin) }
		}, false)},
		{name: "fused-go", noFactoryConnection: true, start: func(t *testing.T) shutdownPeer {
			extensionConn, hostConn := net.Pipe()
			done := make(chan error, 1)
			go func() { done <- earlydispatch.Extension().RunWithConn(extensionConn) }()
			t.Cleanup(func() { _ = hostConn.Close() })
			return shutdownPeer{conn: hostConn, end: func(t *testing.T) processEnd {
				t.Helper()
				select {
				case err := <-done:
					return processEnd{err: err}
				case <-time.After(testbudget.Wait(t)):
					t.Fatalf("Run did not return within %s of the host's shutdown", testbudget.Wait(t))
					return processEnd{}
				}
			}}
		}},
		{name: "rust", start: isolated(func(t *testing.T) func(string) *exec.Cmd {
			bin, err := rustEarlyDispatchBinary.Do(t, buildRustEarlyDispatchBinary)
			if err != nil {
				t.Fatal(err)
			}
			return func(string) *exec.Cmd { return exec.Command(bin) }
		}, false)},
		{name: "python", start: isolated(func(t *testing.T) func(string) *exec.Cmd {
			script := filepath.Join(data, "early-dispatch-python", "early.py")
			return func(string) *exec.Cmd { return exec.Command(testPythonExecutable(), script) }
		}, false)},
		{name: "lisp", start: isolated(func(t *testing.T) func(string) *exec.Cmd {
			requireSBCL(t)
			sbcl := toolchain.SBCLExecutable("", exec.LookPath)
			script := filepath.Join(data, "early-dispatch-lisp", "early.lisp")
			return func(string) *exec.Cmd {
				return exec.Command(sbcl, "--noinform", "--non-interactive", "--no-sysinit", "--no-userinit", "--disable-debugger", "--load", script)
			}
		}, false)},
		{name: "node", node: true, start: isolated(func(t *testing.T) func(string) *exec.Cmd {
			node := requireConformanceTool(t, "node")
			entry := filepath.Join(data, "early-dispatch-node", "early.mjs")
			cli := filepath.Join(root, "coding", "extension", "host", "subprocess", "runtime-node", "cli.mjs")
			return func(string) *exec.Cmd {
				cmd := exec.Command(node, cli, entry)
				// A Node realm keeps pi.events in its own heap unless the Host routes the bus, as it does once a second realm joins.
				cmd.Env = []string{"PIG_EVENT_BUS=routed"}
				return cmd
			}
		}, true)},
	}
	for _, target := range targets {
		for _, c := range shutdownCases {
			t.Run("isolated/"+target.name+"/"+c.name, func(t *testing.T) {
				if c.factoryEmit && target.noFactoryConnection {
					t.Skip("the placement has no factory connection")
				}
				// The fixtures read their switches from the environment, which every process and the fused extension inherit.
				if c.factoryEmit {
					t.Setenv(earlydispatch.FactoryEmitEnv, "1")
				}
				listenerDir := t.TempDir()
				if c.pureListener {
					t.Setenv(earlydispatch.PureListenerEnv, listenerDir)
				}
				peer := target.start(t)
				if err := peer.conn.SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
					t.Fatal(err)
				}
				c.run(t, peer.conn, target.node)
				requireCleanEnd(t, peer.end(t))
				if c.after != nil {
					c.after(t, listenerDir)
				}
			})
		}
	}
	for _, language := range []string{"go", "rust", "python", "lisp", "node"} {
		t.Run("packed/"+language, func(t *testing.T) {
			runPackedShutdownBeforeReady(t, language)
		})
	}
}

// requireEndOfConnection requires that the extension sends nothing more and ends its side of the connection.
func requireEndOfConnection(t *testing.T, conn net.Conn) {
	t.Helper()
	env, err := tryReadSDKEnvelope(conn)
	if err == nil {
		t.Fatalf("frame %s %+v after the shutdown, want the extension to end the connection", env.Type, env)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("read after the shutdown: %v, want the extension's end of the connection (io.EOF); a timeout means it ignored the shutdown", err)
	}
}

// requireShutdownEndsTheConnection shuts down the extension of conn, which has registered, and requires that it ends its side of the connection. The extension answers a ping first, so it is alive and reading the frames of its load. The script never closes its own end, so only the shutdown can end the connection.
func requireShutdownEndsTheConnection(t *testing.T, conn net.Conn) {
	t.Helper()
	nonce := fmt.Sprintf("before-shutdown-%p", conn)
	writeSDKEnvelope(t, conn, subprocess.Envelope{Type: subprocess.MsgPing, Ping: &subprocess.PingPayload{Nonce: nonce}})
	if pong := readSDKEnvelope(t, conn); pong.Type != subprocess.MsgPong || pong.Pong == nil || pong.Pong.Nonce != nonce {
		t.Fatalf("answer to the ping = %+v, want a pong carrying %q", pong, nonce)
	}
	writeSDKEnvelope(t, conn, subprocess.Envelope{Type: subprocess.MsgShutdown, Shutdown: &subprocess.ShutdownPayload{Reason: "reload"}})
	requireEndOfConnection(t, conn)
}

func requireCleanEnd(t *testing.T, end processEnd) {
	t.Helper()
	if end.err != nil || end.stderr != "" {
		t.Fatalf("the extension ended with %v and stderr %q, want exit status 0 and nothing on stderr", end.err, end.stderr)
	}
}

// packedShutdownMembers are the two members of the cell: the first is shut down before ready, the second is served normally.
var packedShutdownMembers = []string{"shutdown_first", "shutdown_second"}

func runPackedShutdownBeforeReady(t *testing.T, language string) {
	t.Helper()
	start := packedShutdownRunner(t, language)
	process := startSDKProcessListening(t, len(packedShutdownMembers), start, language == "node")
	first := process.accept(t, 0)
	if err := first.SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
		t.Fatal(err)
	}
	node := language == "node"
	readUntilRegister(t, first, node)

	// The second member loads and is marked ready while the first still waits for its ready: a Node runtime serves one member's factory calls on the loop it shares with the other, so the second member's calls are answered before the first is shut down. The cell is not the first member's to end: the second member is served after the first ended its run.
	second := process.accept(t, 1)
	if err := second.SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
		t.Fatal(err)
	}
	readUntilRegister(t, second, node)
	writeSDKEnvelope(t, second, subprocess.Envelope{Type: subprocess.MsgReady, Ready: &subprocess.ReadyPayload{Cwd: "/fixture", Width: 80, Height: 24, State: &subprocess.StatePayload{HasUI: true}}})

	requireShutdownEndsTheConnection(t, first)
	requireShutdownEndsTheConnection(t, second)
	requireCleanEnd(t, process.end(t))
}

// packedShutdownRunner builds the packed cell of language holding the members and returns the command that runs it with one socket for each.
func packedShutdownRunner(t *testing.T, language string) func(addresses []string) *exec.Cmd {
	t.Helper()
	root := findModuleRoot(t)
	cacheRoot := filepath.Join(os.Getenv("PIG_HOME"), "cache")
	sources := filepath.Join(sharedSources, "packed-shutdown-"+language)
	write := func(path, text string) {
		t.Helper()
		if err := writeSharedSource(path, text); err != nil {
			t.Fatal(err)
		}
	}
	var binary string
	var args []string
	switch language {
	case "go":
		exts := make([]runtimecell.GoExtension, 0, len(packedShutdownMembers))
		for _, name := range packedShutdownMembers {
			dir := filepath.Join(sources, name)
			module := "example.com/" + name
			write(filepath.Join(dir, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", module))
			write(filepath.Join(dir, "ext.go"), fmt.Sprintf(`package ext

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New(%q)
	if _, err := e.Events().On(%q, func(ctx sdk.Context, data any) error { return ctx.Events().Emit(%q, data) }); err != nil {
		panic(err)
	}
	return e
}
`, name, earlydispatch.Channel, earlydispatch.EchoChannel))
			exts = append(exts, runtimecell.GoExtension{Name: name, Root: dir, ModulePath: module, Package: module, Factory: "Extension", Hash: "shutdown-" + name})
		}
		cell, err := runtimecell.BuildGoPackedCell(t.Context(), cacheRoot, "shutdown-before-ready-go", exts)
		if err != nil {
			t.Fatal(err)
		}
		binary = cell.BinaryPath
	case "rust":
		exts := make([]runtimecell.RustExtension, 0, len(packedShutdownMembers))
		for _, name := range packedShutdownMembers {
			dir := filepath.Join(sources, name)
			write(filepath.Join(dir, "Cargo.toml"), fmt.Sprintf("[package]\nname = %q\nversion = \"0.1.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = %q }\n", name, filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs"))))
			write(filepath.Join(dir, "src", "lib.rs"), fmt.Sprintf(`pub fn new_extension() -> pig_sdk::Extension {
    let ext = pig_sdk::Extension::new(%q);
    ext.events()
        .on(%q, |ctx, data| ctx.events().emit(%q, data.clone()).map_err(|err| err.to_string()))
        .unwrap();
    ext
}
`, name, earlydispatch.Channel, earlydispatch.EchoChannel))
			exts = append(exts, runtimecell.RustExtension{Name: name, Root: dir, Package: name, Factory: "new_extension", Hash: "shutdown-" + name})
		}
		cell, err := runtimecell.BuildRustPackedCell(t.Context(), cacheRoot, "shutdown-before-ready-rust", exts)
		if err != nil {
			t.Fatal(err)
		}
		binary = cell.BinaryPath
	case "python":
		exts := make([]runtimecell.PythonExtension, 0, len(packedShutdownMembers))
		for _, name := range packedShutdownMembers {
			dir := filepath.Join(sources, name)
			write(filepath.Join(dir, name+".py"), fmt.Sprintf(`import pig_sdk


def new_extension():
    e = pig_sdk.Extension(%q)
    e.events.on(%q, lambda ctx, data: ctx.events.emit(%q, data))
    return e
`, name, earlydispatch.Channel, earlydispatch.EchoChannel))
			exts = append(exts, runtimecell.PythonExtension{Name: name, Root: dir, Package: name, Factory: "new_extension", Hash: "shutdown-" + name})
		}
		cell, err := runtimecell.BuildPythonPackedCell(t.Context(), cacheRoot, "shutdown-before-ready-python", exts)
		if err != nil {
			t.Fatal(err)
		}
		binary, args = testPythonExecutable(), []string{cell.BinaryPath}
	case "lisp":
		requireSBCL(t)
		exts := make([]runtimecell.LispExtension, 0, len(packedShutdownMembers))
		for _, name := range packedShutdownMembers {
			dir := filepath.Join(sources, name)
			write(filepath.Join(dir, name+".lisp"), fmt.Sprintf(`(defpackage %q (:use "COMMON-LISP"))
(in-package %q)
(defun new-extension ()
  (let ((e (pig:make-extension %q)))
    (pig:events-on (pig:extension-events e) %q
                   (lambda (ctx data) (pig:events-emit (pig:ctx-events ctx) %q data)))
    e))
`, name, name, name, earlydispatch.Channel, earlydispatch.EchoChannel))
			exts = append(exts, runtimecell.LispExtension{Name: name, Root: dir, Package: name, Factory: "new-extension", Hash: "shutdown-" + name})
		}
		cell, err := runtimecell.BuildLispPackedCell(t.Context(), cacheRoot, "shutdown-before-ready-lisp", exts)
		if err != nil {
			t.Fatal(err)
		}
		binary = cell.BinaryPath
	case "node":
		node := requireConformanceTool(t, "node")
		runtimeDir := filepath.Join(root, "coding", "extension", "host", "subprocess", "runtime-node")
		entry := filepath.Join(root, "test", "extension-conformance", "testdata", "early-dispatch-node", "early.mjs")
		var manifest []string
		for _, name := range packedShutdownMembers {
			manifest = append(manifest, fmt.Sprintf(`{"name":%q,"entry":%q,"sockEnv":%q}`, name, entry, runtimecell.SocketEnvName(name)))
		}
		manifestPath := filepath.Join(sources, "manifest.json")
		write(manifestPath, "["+strings.Join(manifest, ",")+"]")
		binary, args = node, []string{filepath.Join(runtimeDir, "cell.mjs"), manifestPath, entry, entry}
	default:
		t.Fatalf("no packed cell for %s", language)
	}
	return func(addresses []string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		cmd.Env = os.Environ()
		for i, name := range packedShutdownMembers {
			cmd.Env = append(cmd.Env, runtimecell.SocketEnvName(name)+"="+addresses[i])
		}
		if language == "node" {
			// The host admits each Node member over the process's stdin, and a realm keeps pi.events in its own heap unless the Host routes the bus.
			var lines []string
			for i, name := range packedShutdownMembers {
				lines = append(lines, strings.Join([]string{"admit", name, addresses[i], args[2+i], "", "0"}, "\t"))
			}
			cmd.Env = append(cmd.Env, "PIG_EVENT_BUS=routed")
			cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
		}
		return cmd
	}
}
