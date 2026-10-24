//go:build unix

package env

// Differential tests of exec, hello, output ordering and watch against Pi's own Rust daemon: see daemon_wire_oracle_test.go.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/env/daemon"
)

// windowFiles are the outputs the window cases print with one write each, so both daemons see the same chunks.
func windowFiles(t *testing.T, root string) {
	t.Helper()
	var lines, multibyte strings.Builder
	for index := 1; index <= 100; index++ {
		fmt.Fprintf(&lines, "line %d\n", index)
	}
	for index := 1; index <= 60; index++ {
		fmt.Fprintf(&multibyte, "é😀%d\n", index)
	}
	mustDo(os.WriteFile(filepath.Join(root, "lines100.txt"), []byte(lines.String()), 0o644))
	mustDo(os.WriteFile(filepath.Join(root, "lines-tail.txt"), []byte(lines.String()+"tail"), 0o644))
	mustDo(os.WriteFile(filepath.Join(root, "multibyte.txt"), []byte(multibyte.String()), 0o644))
	mustDo(os.WriteFile(filepath.Join(root, "longline.txt"), []byte(strings.Repeat("x", 500)), 0o644))
}

var spillName = wireNormalizer{regexp.MustCompile(`tmp-[A-Za-z0-9]{6}/pi-output-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.log`), `tmp-XXXXXX/pi-output-UUID.log`}

// renderExec is render for a command: output events of one stream are one text (the pipes' chunking is the system's), then the
// settled frame, then the spill file the daemon names, read from disk.
func renderExec(frames []*daemon.Frame, root string, merge bool, normalizers []wireNormalizer) string {
	events := frames[:len(frames)-1]
	last := frames[len(frames)-1]
	var shown []*daemon.Frame
	if merge {
		merged := map[string][]byte{}
		for _, event := range events {
			stream, _ := event.JSON.(daemon.Object)["stream"].(string)
			merged[stream] = append(merged[stream], event.Payload...)
		}
		for _, stream := range []string{"stdout", "stderr"} {
			if text, ok := merged[stream]; ok {
				shown = append(shown, &daemon.Frame{Kind: daemon.FrameEvent, JSON: daemon.Object{"kind": "output", "stream": stream}, Payload: text})
			}
		}
	} else {
		shown = events
	}
	text := render(append(shown, last), root, normalizers)
	if object, ok := last.JSON.(daemon.Object); ok {
		if path, ok := object["spillPath"].(string); ok {
			content, err := os.ReadFile(path)
			text += fmt.Sprintf("spill file %q %v\n", digest(content), err)
		}
	}
	return text
}

func digest(content []byte) string {
	if len(content) <= 4096 {
		return string(content)
	}
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%d bytes sha256 %s", len(content), hex.EncodeToString(sum[:]))
}

// execCase is one command request; its events are merged per stream unless window is set.
type execCase struct {
	name    string
	request daemon.Object
	setup   func(t *testing.T, root string)
	// window keeps the event frames apart: the request asks for an output window.
	window bool
	// extraEnv is added to the daemon's environment.
	extraEnv []string
}

func execCases() []execCase {
	sh := func(command string, more ...any) daemon.Object {
		request := object("op", "exec", "cwd", "{ROOT}", "command", command)
		for index := 0; index < len(more); index += 2 {
			request[more[index].(string)] = more[index+1]
		}
		return request
	}
	argv := func(args ...any) daemon.Object { return object("op", "exec", "cwd", "{ROOT}", "argv", args) }
	window := func(maxBytes, maxLines int) daemon.Object {
		return object("maxBytes", maxBytes, "maxLines", maxLines, "minIntervalMs", 0, "bytesPerSecond", 1e9)
	}
	return []execCase{
		{name: "stdout and exit code", request: sh("echo out; exit 3")},
		{name: "stderr", request: sh("echo err >&2; exit 0")},
		{name: "both streams", request: sh("echo out; echo err >&2")},
		{name: "killed by a signal", request: sh("kill -9 $$")},
		{name: "terminated by a signal", request: sh("kill -TERM $$")},
		{name: "shell is bash", request: sh("echo $0")},
		{name: "shell path", request: sh("echo $0", "shellPath", "/bin/sh")},
		{name: "missing shell path", request: sh("echo x", "shellPath", "{ROOT}/no-such-shell")},
		{name: "argv", request: argv("sh", "-c", "printf a; printf b >&2; exit 2")},
		{name: "argv missing program", request: argv("{ROOT}/no-such-program")},
		{name: "argv not executable", request: argv("{ROOT}/file.txt"), setup: fsTree},
		{name: "argv program is a directory", request: argv("{ROOT}/dir"), setup: fsTree},
		{name: "empty argv", request: object("op", "exec", "cwd", "{ROOT}", "argv", []any{})},
		{name: "non-string argv", request: object("op", "exec", "cwd", "{ROOT}", "argv", []any{"echo", 1})},
		{name: "neither command nor argv", request: object("op", "exec", "cwd", "{ROOT}")},
		{name: "no cwd", request: object("op", "exec", "command", "echo")},
		{name: "missing cwd", request: object("op", "exec", "cwd", "{ROOT}/missing", "command", "echo x")},
		{name: "cwd is a file", request: object("op", "exec", "cwd", "{ROOT}/file.txt", "command", "echo x"), setup: fsTree},
		{name: "command wins over argv", request: object("op", "exec", "cwd", "{ROOT}", "command", "echo command", "argv", []any{"echo", "argv"})},
		{name: "working directory", request: sh("pwd")},
		{name: "environment added", request: sh("echo $FOO-$HOME", "env", object("FOO", "bar", "N", 7, "NIL", nil))},
		{name: "environment replaced", request: sh("env", "inheritEnv", false, "env", object("ONLY", "this")), extraEnv: []string{"EXTRA=1"}},
		{name: "inherited environment", request: sh("echo $EXTRA", "inheritEnv", true), extraEnv: []string{"EXTRA=inherited"}},
		{name: "session leader", request: sh(`test "$(ps -o sid= -p $$ | tr -d ' ')" = "$$" && echo leader || echo follower`)},
		{name: "default signals", request: sh(`trap '' INT TERM; sh -c 'kill -TERM $$; echo survived'`)},
		{name: "stdin is closed", request: sh("cat; echo done")},
		{name: "invalid utf-8 and marks", request: sh(`printf '\357\273\277a\377b\342\202'; printf '\357\273\277c' >&2`)},
		{name: "a mark inside the output", request: sh(`printf 'a\357\273\277b'`)},
		{name: "empty output", request: sh("true")},
		{name: "descendant holds the pipes", request: sh("(sleep 3 &) ; echo done")},
		{name: "descendant writes after exit", request: sh("(sleep 0.05; echo late) & echo early")},
		{name: "timeout", request: sh("echo before; sleep 30", "timeoutMs", 300)},
		{name: "timeout with spill", request: sh("echo before; sleep 30", "timeoutMs", 300, "spill", object("afterBytes", 1, "afterLines", 100))},
		{name: "timeout of zero", request: sh("sleep 30", "timeoutMs", 0)},
		{name: "fractional timeout", request: sh("sleep 30", "timeoutMs", 150.5)},
		{name: "timeout not reached", request: sh("echo quick", "timeoutMs", 20000)},
		{name: "spill by bytes", request: sh("printf 0123456789abcdef", "spill", object("afterBytes", 10, "afterLines", 100))},
		{name: "spill by lines", request: sh("printf 'a\\nb\\nc\\n'", "spill", object("afterBytes", 1000, "afterLines", 2))},
		{name: "spill at the thresholds", request: sh("printf 'a\\nb\\nc'", "spill", object("afterBytes", 5, "afterLines", 3))},
		{name: "spill of a final partial line", request: sh("printf 'abc'", "spill", object("afterBytes", 100, "afterLines", 0))},
		{name: "spill never reached", request: sh("echo small", "spill", object("afterBytes", 1000, "afterLines", 1000))},
		{name: "spill missing a field", request: sh("printf 0123456789abcdef", "spill", object("afterBytes", 10))},
		{name: "spill with both streams", request: sh("printf 'aaaa'; sleep 0.2; printf 'bbbb' >&2", "spill", object("afterBytes", 2, "afterLines", 100))},
		{name: "spill where the temporary directory is not writable", request: sh("printf 0123456789abcdef", "spill", object("afterBytes", 3, "afterLines", 3)), extraEnv: []string{"TMPDIR={ROOT}/missing"}},
		{name: "large output", request: sh("head -c 3000000 /dev/zero | tr '\\\\0' a; head -c 200000 /dev/zero | tr '\\\\0' b >&2")},
		{name: "window within bounds", request: sh("echo small", "window", window(1000, 1000)), window: true, setup: windowFiles},
		{name: "window skips older lines", request: sh("cat lines100.txt", "window", window(50, 5)), window: true, setup: windowFiles},
		{name: "window of one line", request: sh("cat lines100.txt", "window", window(5, 1)), window: true, setup: windowFiles},
		{name: "window keeps multibyte characters whole", request: sh("cat multibyte.txt", "window", window(7, 100)), window: true, setup: windowFiles},
		{name: "window without a final newline", request: sh("cat lines-tail.txt", "window", window(40, 4)), window: true, setup: windowFiles},
		{name: "window and spill", request: sh("cat lines100.txt", "window", window(50, 5), "spill", object("afterBytes", 100, "afterLines", 1000)), window: true, setup: windowFiles},
		{name: "window coalesces output of both streams", request: sh("cat lines100.txt; sleep 0.1; cat lines100.txt >&2; sleep 0.1; cat lines-tail.txt; sleep 0.1; cat multibyte.txt >&2", "window", object("maxBytes", 60, "maxLines", 3, "minIntervalMs", 800, "bytesPerSecond", 1e9)), window: true, setup: windowFiles},
		{name: "window of two streams within bounds", request: sh("echo one; sleep 0.1; echo two >&2; sleep 0.1; echo three", "window", object("maxBytes", 1000, "maxLines", 1000, "minIntervalMs", 800, "bytesPerSecond", 1e9)), window: true},
		{name: "window missing a field", request: sh("cat lines100.txt", "window", object("maxBytes", 5, "maxLines", 5, "minIntervalMs", 0)), window: true, setup: windowFiles},
		{name: "window cut by bytes alone", request: sh("cat lines100.txt; sleep 0.1; cat longline.txt >&2; sleep 0.1; cat longline.txt", "window", object("maxBytes", 60, "maxLines", 3, "minIntervalMs", 800, "bytesPerSecond", 1e9)), window: true, setup: windowFiles},
		{name: "window with negative pace", request: sh("cat lines100.txt", "window", object("maxBytes", 50, "maxLines", 5, "minIntervalMs", -5, "bytesPerSecond", -1)), window: true, setup: windowFiles},
	}
}

func TestDaemonExecMatchesPisDaemon(t *testing.T) {
	for _, test := range execCases() {
		t.Run(test.name, func(t *testing.T) {
			oracle := oracleDaemon(t)
			var outputs [2]string
			for index, binary := range []string{oracle, daemonBinary(t)} {
				base := t.TempDir()
				root := filepath.Join(base, "root")
				mustDo(os.MkdirAll(root, 0o755))
				tmp := filepath.Join(base, "tmp")
				mustDo(os.MkdirAll(tmp, 0o755))
				if test.setup != nil {
					test.setup(t, root)
				}
				extra := make([]string, len(test.extraEnv))
				for position, entry := range test.extraEnv {
					extra[position] = strings.ReplaceAll(entry, "{ROOT}", root)
				}
				environment := daemonEnvironment(base, tmp, extra...)
				d := startWireDaemon(t, binary, root, environment)
				frames := d.call(withRoot(test.request, root).(daemon.Object), nil)
				outputs[index] = renderExec(frames, root, !test.window, []wireNormalizer{spillName, {regexp.MustCompile(regexp.QuoteMeta(tmp)), "{TMP}"}, {regexp.MustCompile(regexp.QuoteMeta(base)), "{BASE}"}})
			}
			compareWire(t, test.name, outputs[0], outputs[1])
		})
	}
}

// TestDaemonExecCancelMatchesPisDaemon cancels a running command by abort and by kill, and a cancel that arrives after the end.
func TestDaemonExecCancelMatchesPisDaemon(t *testing.T) {
	for _, mode := range []string{"", "abort", "kill"} {
		t.Run("mode "+mode, func(t *testing.T) {
			pi, pig := runOnBoth(t, nil, func(d *wireDaemon, root string) string {
				id := d.start(object("op", "exec", "cwd", root, "command", "echo started; sleep 30", "spill", object("afterBytes", 1, "afterLines", 100)), nil)
				// The output proves the command runs, so the cancel reaches a live process.
				var frames []*daemon.Frame
				for {
					frame := d.next()
					frames = append(frames, frame)
					if frame.Kind == daemon.FrameEvent {
						break
					}
				}
				cancel := object()
				if mode != "" {
					cancel["mode"] = mode
				}
				d.send(daemon.FrameCancel, id, cancel, nil)
				frames = append(frames, d.settle(id)...)
				// A cancel of a request that is over, and of one that never existed, changes nothing.
				d.send(daemon.FrameCancel, id, cancel, nil)
				d.send(daemon.FrameCancel, 9999, cancel, nil)
				frames = append(frames, d.call(object("op", "lstat", "path", root), nil)...)
				return renderExec(frames, root, true, []wireNormalizer{spillName})
			})
			compareWire(t, "cancel "+mode, pi, pig)
		})
	}
}

// TestDaemonCancelRightBehindItsRequestMatchesPisDaemon: a cancel that follows its request at once is never lost.
func TestDaemonCancelRightBehindItsRequestMatchesPisDaemon(t *testing.T) {
	pi, pig := runOnBoth(t, nil, func(d *wireDaemon, root string) string {
		var text strings.Builder
		for _, mode := range []string{"abort", "kill"} {
			for range 30 {
				id := d.start(object("op", "exec", "cwd", root, "argv", []any{"sleep", "30"}), nil)
				d.send(daemon.FrameCancel, id, object("mode", mode), nil)
				fmt.Fprint(&text, mode, " ", renderExec(d.settle(id), root, true, nil))
			}
		}
		return text.String()
	})
	compareWire(t, "early cancel", pi, pig)
}

// TestDaemonHelloMatchesPisDaemon compares the hello reply under each environment that changes it.
func TestDaemonHelloMatchesPisDaemon(t *testing.T) {
	version := wireNormalizer{regexp.MustCompile(`"version":"[^"]*"`), `"version":"V"`}
	for _, test := range []struct {
		name string
		env  []string
	}{
		{"configured", nil},
		{"tmpdir with a trailing slash", []string{"TMPDIR={TMP}/"}},
		{"tmpdir of one slash", []string{"TMPDIR=/"}},
		{"tmpdir before the others", []string{"TMPDIR={TMP}/a", "TMP={TMP}/b", "TEMP={TMP}/c"}},
		{"tmp when tmpdir is empty", []string{"TMPDIR=", "TMP={TMP}/x"}},
		{"temp when the others are empty", []string{"TMPDIR=", "TMP=", "TEMP=/t/e/m/p"}},
		{"none set", []string{"TMPDIR=", "TMP=", "TEMP="}},
		{"no home", []string{"HOME="}},
	} {
		t.Run(test.name, func(t *testing.T) {
			oracle := oracleDaemon(t)
			var outputs [2]string
			for index, binary := range []string{oracle, daemonBinary(t)} {
				base := t.TempDir()
				root := filepath.Join(base, "root")
				mustDo(os.MkdirAll(root, 0o755))
				tmp := filepath.Join(base, "tmp")
				mustDo(os.MkdirAll(tmp, 0o755))
				environment := []string{"HOME=" + base, "PATH=" + os.Getenv("PATH")}
				for _, entry := range test.env {
					environment = append(environment, strings.ReplaceAll(entry, "{TMP}", tmp))
				}
				d := startWireDaemon(t, binary, root, environment)
				text := render(d.call(object("op", "hello"), nil), root, []wireNormalizer{version, {regexp.MustCompile(regexp.QuoteMeta(base)), "{BASE}"}})
				outputs[index] = d.sync + text
			}
			compareWire(t, test.name, outputs[0], outputs[1])
		})
	}
}

// TestDaemonRepliesBeforeBulkOutputLikePisDaemon: a small reply goes ahead of command output that is queued behind a client that
// does not read. Output is read only after the request is answered, so the queued bulk output is what the reply jumps.
func TestDaemonRepliesBeforeBulkOutputLikePisDaemon(t *testing.T) {
	// The command has written 3.2 MB when the request is sent, so at least 1.9 MB of it waits in the daemon behind what its
	// output buffer (1 MiB) and the pipe (64 KiB) hold. A reply that waited its turn would come after that much output.
	const bound = 1_600_000
	oracle := oracleDaemon(t)
	for name, binary := range map[string]string{"pi": oracle, "pig": daemonBinary(t)} {
		base := t.TempDir()
		root := filepath.Join(base, "root")
		mustDo(os.MkdirAll(root, 0o755))
		d := startWireDaemon(t, binary, root, daemonEnvironment(base, base))
		started := filepath.Join(root, "started")
		execID := d.start(object("op", "exec", "cwd", root, "command", "head -c 3200000 /dev/zero; touch started; head -c 20000000 /dev/zero"), nil)
		for deadline := time.Now().Add(30 * time.Second); ; {
			if _, err := os.Stat(started); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the command never produced its first 3.2 MB", name)
			}
			time.Sleep(10 * time.Millisecond)
		}
		infoID := d.start(object("op", "lstat", "path", root), nil)
		before := 0
		for {
			frame := d.next()
			if frame.ID == infoID {
				break
			}
			if frame.ID != execID || frame.Kind != daemon.FrameEvent {
				t.Fatalf("%s: unexpected frame %+v", name, frame)
			}
			before += len(frame.Payload)
		}
		total := before
		for {
			frame := d.next()
			if frame.Kind == daemon.FrameEvent {
				total += len(frame.Payload)
			}
			if frame.Kind == daemon.FrameResult {
				break
			}
		}
		if total != 3_200_000+20_000_000 {
			t.Fatalf("%s: %d bytes of output arrived", name, total)
		}
		if before > bound {
			t.Errorf("%s: %d bytes of queued output went ahead of the reply, more than the %d a client's buffers hold", name, before, bound)
		}
	}
}

func waitFor(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// watchCase is a watch request and the changes made under it; each change is one file system operation.
type watchCase struct {
	name    string
	request daemon.Object
	setup   func(t *testing.T, root string)
	changes []func(t *testing.T, root string)
	// single makes each change answer with exactly one change event: a sentinel file would not be seen by a watch of one file.
	single bool
}

func inRoot(operation func(root string) error) func(t *testing.T, root string) {
	return func(t *testing.T, root string) {
		t.Helper()
		if err := operation(root); err != nil {
			t.Fatal(err)
		}
	}
}

func watchTree(t *testing.T, root string) {
	t.Helper()
	mustDo(os.MkdirAll(filepath.Join(root, "watched", "sub", "deep"), 0o755))
	mustDo(os.MkdirAll(filepath.Join(root, "staging", "tree", "inner"), 0o755))
	mustDo(os.WriteFile(filepath.Join(root, "watched", "a.txt"), []byte("a"), 0o644))
	mustDo(os.WriteFile(filepath.Join(root, "watched", ".hidden"), []byte("h"), 0o644))
	mustDo(os.WriteFile(filepath.Join(root, "watched", "sub", "b.txt"), []byte("b"), 0o644))
	mustDo(os.WriteFile(filepath.Join(root, "watched", "sub", "deep", "c.txt"), []byte("c"), 0o644))
	mustDo(os.WriteFile(filepath.Join(root, "staging", "tree", "x1"), []byte("1"), 0o644))
	mustDo(os.WriteFile(filepath.Join(root, "staging", "tree", "inner", "x2"), []byte("2"), 0o644))
	mustDo(os.WriteFile(filepath.Join(root, "staging", "z"), []byte("z"), 0o644))
	mustDo(os.WriteFile(filepath.Join(root, "other.txt"), []byte("o"), 0o644))
}

func watchCases() []watchCase {
	target := func(options ...any) daemon.Object {
		entry := object("path", "{ROOT}/watched")
		for index := 0; index < len(options); index += 2 {
			entry[options[index].(string)] = options[index+1]
		}
		return entry
	}
	watch := func(mode string, targets ...any) daemon.Object {
		request := object("op", "watch", "targets", targets, "pollIntervalMs", 40)
		if mode != "" {
			request["mode"] = mode
		}
		return request
	}
	create := func(name string) func(t *testing.T, root string) {
		return inRoot(func(root string) error { return os.WriteFile(filepath.Join(root, name), []byte("new"), 0o644) })
	}
	modify := func(name string) func(t *testing.T, root string) {
		return inRoot(func(root string) error {
			return os.WriteFile(filepath.Join(root, name), []byte("changed content"), 0o644)
		})
	}
	remove := func(name string) func(t *testing.T, root string) {
		return inRoot(func(root string) error { return os.RemoveAll(filepath.Join(root, name)) })
	}
	rename := func(from, to string) func(t *testing.T, root string) {
		return inRoot(func(root string) error { return os.Rename(filepath.Join(root, from), filepath.Join(root, to)) })
	}
	var cases []watchCase
	for _, mode := range []string{"polling", "native", ""} {
		cases = append(cases,
			watchCase{name: "recursive changes " + mode, setup: watchTree, request: watch(mode, target("recursive", true)), changes: []func(*testing.T, string){
				create("watched/new.txt"), modify("watched/a.txt"), remove("watched/a.txt"), create("watched/sub/deep/n.txt"),
				rename("watched/sub/deep/n.txt", "watched/sub/m.txt"), rename("staging/tree", "watched/tree"), remove("watched/tree"),
				create("watched/.hidden2"), create("other2.txt"), rename("staging/z", "watched/z"), rename("watched/z", "staging/z"),
			}},
			watchCase{name: "flat changes " + mode, setup: watchTree, request: watch(mode, target()), changes: []func(*testing.T, string){
				create("watched/new.txt"), create("watched/sub/ignored.txt"), modify("watched/a.txt"), rename("staging/tree", "watched/tree"),
			}},
			watchCase{name: "excluded names " + mode, setup: watchTree, request: watch(mode, target("recursive", true, "exclude", object("hidden", true, "names", []any{"skip.txt", "sub"}))), changes: []func(*testing.T, string){
				create("watched/skip.txt"), create("watched/.hidden3"), create("watched/sub/b2.txt"), create("watched/kept.txt"), rename("staging/tree", "watched/tree"),
			}},
			watchCase{name: "watch a file " + mode, setup: watchTree, request: watch(mode, object("path", "{ROOT}/watched/a.txt")), single: true, changes: []func(*testing.T, string){
				modify("watched/a.txt"), remove("watched/a.txt"), create("watched/a.txt"), modify("watched/a.txt"),
			}},
			watchCase{name: "several targets " + mode, setup: watchTree, request: watch(mode, target(), object("path", "{ROOT}/staging", "recursive", true)), changes: []func(*testing.T, string){
				create("watched/n1"), create("staging/tree/inner/n2"), create("other3.txt"), rename("staging/tree", "watched/tree2"),
			}},
		)
	}
	return cases
}

type watchEvents struct {
	ready   string
	changes []string
}

// TestDaemonWatchMatchesPisDaemon makes the same changes under a watch of each daemon: the ready event, and the paths each change
// reports, in the order the daemon sorts them. A sentinel file made after each change marks the end of its events.
func TestDaemonWatchMatchesPisDaemon(t *testing.T) {
	for _, test := range watchCases() {
		t.Run(test.name, func(t *testing.T) {
			pi, pig := runOnBoth(t, test.setup, func(d *wireDaemon, root string) string {
				id := d.start(withRoot(test.request, root).(daemon.Object), nil)
				first := d.next()
				var text strings.Builder
				text.WriteString(render([]*daemon.Frame{first}, root, nil))
				if first.Kind != daemon.FrameEvent {
					return text.String()
				}
				changes := append(test.changes, nil)
				if test.single {
					changes = test.changes
				}
				for index, change := range changes {
					sentinel := fmt.Sprintf("watched/zz-sentinel-%d", index)
					if change != nil {
						change(t, root)
					}
					if !test.single {
						mustDo(os.WriteFile(filepath.Join(root, sentinel), nil, 0o644))
					}
					var paths []string
					for done := false; !done; {
						frame := d.next()
						if frame.ID != id || frame.Kind != daemon.FrameEvent {
							t.Fatalf("unexpected frame %+v", frame)
						}
						event := frame.JSON.(daemon.Object)
						if event["kind"] != "change" {
							fmt.Fprintf(&text, "change %d: %v\n", index, event)
							done = true
							continue
						}
						for _, path := range event["paths"].([]any) {
							if path.(string) == filepath.Join(root, sentinel) {
								done = true
							} else {
								paths = append(paths, strings.ReplaceAll(path.(string), root, "{ROOT}"))
							}
						}
						if test.single {
							done = true
						}
					}
					fmt.Fprintf(&text, "change %d: %q\n", index, paths)
				}
				d.send(daemon.FrameCancel, id, object(), nil)
				// The remaining events are the ones the changes caused after the sentinel; the end is the result.
				for {
					frame := d.next()
					if frame.Kind == daemon.FrameResult || frame.Kind == daemon.FrameError {
						text.WriteString(render([]*daemon.Frame{frame}, root, nil))
						break
					}
				}
				return text.String()
			})
			compareWire(t, test.name, pi, pig)
		})
	}
}

// TestDaemonWatchRequestsMatchPisDaemon compares the answers to watch requests that fail before they start, and the end of a watch
// whose root goes away.
func TestDaemonWatchRequestsMatchPisDaemon(t *testing.T) {
	for _, test := range []watchCase{
		{name: "no targets", request: object("op", "watch")},
		{name: "targets not an array", request: object("op", "watch", "targets", "x")},
		{name: "target without a path", request: object("op", "watch", "targets", []any{object("recursive", true)})},
		{name: "missing target", request: object("op", "watch", "targets", []any{object("path", "{ROOT}/missing")})},
		{name: "target below a file", request: object("op", "watch", "targets", []any{object("path", "{ROOT}/other.txt/x")}), setup: watchTree},
		{name: "too many directories", setup: watchTree, request: object("op", "watch", "targets", []any{object("path", "{ROOT}/watched", "recursive", true)}, "maxDirectories", 2)},
		{name: "directory limit exactly met", setup: watchTree, request: object("op", "watch", "targets", []any{object("path", "{ROOT}/watched", "recursive", true)}, "maxDirectories", 3, "mode", "polling", "pollIntervalMs", 40)},
		{name: "empty targets", request: object("op", "watch", "targets", []any{}, "mode", "polling")},
	} {
		t.Run(test.name, func(t *testing.T) {
			pi, pig := runOnBoth(t, test.setup, func(d *wireDaemon, root string) string {
				id := d.start(withRoot(test.request, root).(daemon.Object), nil)
				first := d.next()
				text := render([]*daemon.Frame{first}, root, nil)
				if first.Kind == daemon.FrameEvent {
					d.send(daemon.FrameCancel, id, object(), nil)
					text += render([]*daemon.Frame{d.next()}, root, nil)
				}
				return text
			})
			compareWire(t, test.name, pi, pig)
		})
	}
}
