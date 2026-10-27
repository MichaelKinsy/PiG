//go:build unix

package env

// Differential tests of the daemon's wire behavior against Pi's own Rust daemon (packages/env/daemon). Both daemons run the same
// scripted requests over their stdin and stdout frames, in trees built alike, and every reply (result or error JSON, events and
// payload bytes) must be equal once the temporary root and the values the system chooses (inode, device, times, process ids,
// random names) are replaced by placeholders. The expectation is what Pi's daemon answered, not what PiG's does.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/env/daemon"
)

// wireDaemon is one daemon process driven frame by frame.
type wireDaemon struct {
	t      *testing.T
	binary string
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *os.File
	reader *bufio.Reader
	nextID uint32
	// sync is the line the daemon printed before its first frame.
	sync string
}

// startWireDaemon starts binary as `serve --token tok` with exactly the environment given, in cwd.
func startWireDaemon(t *testing.T, binary, cwd string, environment []string) *wireDaemon {
	t.Helper()
	return startWireDaemonWithSoftLimit(t, binary, cwd, environment, "")
}

// startWireDaemonWithSoftLimit is startWireDaemon with the soft limit of open files set to softLimit (a count the shell's
// ulimit accepts) instead of the hard limit; the empty string means the hard limit.
func startWireDaemonWithSoftLimit(t *testing.T, binary, cwd string, environment []string, softLimit string) *wireDaemon {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// The soft limit of open files goes to the hard one: Go raises it by itself, Pi's daemon does not, and the handle limit is what is under test.
	script := `ulimit -n "$(ulimit -Hn)" 2>/dev/null; exec "$0" "$@"`
	if softLimit != "" {
		script = "ulimit -S -n " + softLimit + `; exec "$0" "$@"`
	}
	command := exec.Command("sh", "-c", script, binary, "serve", "--token", "tok")
	command.Dir = cwd
	command.Env = environment
	command.Stdout = write
	command.Stderr = os.Stderr
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("%s: %v", binary, err)
	}
	_ = write.Close()
	d := &wireDaemon{t: t, binary: binary, cmd: command, in: input, out: read, reader: bufio.NewReaderSize(read, 1<<20), nextID: 1}
	t.Cleanup(func() {
		_ = input.Close()
		done := make(chan struct{})
		go func() {
			_ = command.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
		_ = read.Close()
	})
	_ = read.SetReadDeadline(time.Now().Add(30 * time.Second))
	line, err := d.reader.ReadString('\n')
	if err != nil {
		t.Fatalf("sync line of %s: %v", binary, err)
	}
	d.sync = line
	return d
}

func (d *wireDaemon) send(kind byte, id uint32, body any, payload []byte) {
	d.t.Helper()
	if err := daemon.WriteFrame(d.in, &daemon.Frame{Kind: kind, ID: id, JSON: body, Payload: payload}); err != nil {
		d.t.Fatalf("send: %v", err)
	}
}

// start sends request without waiting and returns its id.
func (d *wireDaemon) start(request daemon.Object, payload []byte) uint32 {
	d.t.Helper()
	id := d.nextID
	d.nextID++
	d.send(daemon.FrameRequest, id, request, payload)
	return id
}

// next is the next frame that is not a ping; it fails the test after 30 s of silence.
func (d *wireDaemon) next() *daemon.Frame {
	d.t.Helper()
	for {
		_ = d.out.SetReadDeadline(time.Now().Add(30 * time.Second))
		frame, err := daemon.ReadFrame(d.reader)
		if err != nil || frame == nil {
			d.t.Fatalf("the output of %s ended or stalled: %v", d.binary, err)
		}
		if frame.Kind != daemon.FramePing {
			return frame
		}
	}
}

// settle returns every frame of request id up to and including its result or error.
func (d *wireDaemon) settle(id uint32) []*daemon.Frame {
	d.t.Helper()
	var frames []*daemon.Frame
	for {
		frame := d.next()
		if frame.ID != id {
			d.t.Fatalf("a frame of request %d arrived while waiting for %d: %+v", frame.ID, id, frame)
		}
		frames = append(frames, frame)
		if frame.Kind == daemon.FrameResult || frame.Kind == daemon.FrameError {
			return frames
		}
	}
}

func (d *wireDaemon) call(request daemon.Object, payload []byte) []*daemon.Frame {
	d.t.Helper()
	return d.settle(d.start(request, payload))
}

var frameKindNames = map[byte]string{daemon.FrameResult: "result", daemon.FrameError: "error", daemon.FrameEvent: "event"}

// wireNormalizer rewrites a value the system chooses, so two runs of one request compare equal.
type wireNormalizer struct {
	pattern *regexp.Regexp
	replace string
}

var defaultNormalizers = []wireNormalizer{
	{regexp.MustCompile(`"(mtimeSec|mtimeNsec|dev|ino)":-?[0-9.e+-]+`), `"$1":N`},
	{regexp.MustCompile(`"(pid)":[0-9]+`), `"$1":N`},
}

// render is the comparable text of frames: each frame's kind, JSON (keys sorted) and payload.
func render(frames []*daemon.Frame, root string, normalizers []wireNormalizer) string {
	return renderWith(frames, root, append(append([]wireNormalizer(nil), defaultNormalizers...), normalizers...))
}

// renderWith is render with exactly the normalizers given.
func renderWith(frames []*daemon.Frame, root string, normalizers []wireNormalizer) string {
	var text strings.Builder
	for _, frame := range frames {
		encoded, err := json.Marshal(frame.JSON)
		if err != nil {
			panic(err)
		}
		line := fmt.Sprintf("%s %s payload=%q\n", frameKindNames[frame.Kind], encoded, frame.Payload)
		line = strings.ReplaceAll(line, root, "{ROOT}")
		for _, normalizer := range normalizers {
			line = normalizer.pattern.ReplaceAllString(line, normalizer.replace)
		}
		text.WriteString(line)
	}
	return text.String()
}

// withRoot copies value with "{ROOT}" in every string replaced by root.
func withRoot(value any, root string) any {
	switch typed := value.(type) {
	case string:
		return strings.ReplaceAll(typed, "{ROOT}", root)
	case daemon.Object:
		copied := daemon.Object{}
		for key, item := range typed {
			copied[key] = withRoot(item, root)
		}
		return copied
	case []any:
		copied := make([]any, len(typed))
		for index, item := range typed {
			copied[index] = withRoot(item, root)
		}
		return copied
	default:
		return value
	}
}

// wireStep is one request of a case; payload is its binary part.
type wireStep struct {
	request daemon.Object
	payload []byte
}

func step(request daemon.Object) wireStep { return wireStep{request: request} }

// wireCase is a script of requests over a tree that setup builds identically under each daemon's root.
type wireCase struct {
	name        string
	setup       func(t *testing.T, root string)
	steps       []wireStep
	normalizers []wireNormalizer
	// needsUser skips the case for root, which no permission stops.
	needsUser bool
}

// daemonEnvironment is the environment both daemons start with: nothing of the caller's but PATH.
func daemonEnvironment(home, tmp string, extra ...string) []string {
	environment := []string{"HOME=" + home, "TMPDIR=" + tmp, "PATH=" + os.Getenv("PATH"), "LANG=C"}
	return append(environment, extra...)
}

// bothDaemons runs body once per daemon with a fresh scoped tree and returns Pi's and PiG's outputs.
func runOnBoth(t *testing.T, setup func(t *testing.T, root string), body func(d *wireDaemon, root string) string) (pi, pig string) {
	t.Helper()
	oracle := oracleDaemon(t)
	for _, daemon := range []struct {
		name   string
		binary string
		out    *string
	}{{"pi", oracle, &pi}, {"pig", daemonBinary(t), &pig}} {
		base := t.TempDir()
		root := filepath.Join(base, "root")
		mustDo(os.MkdirAll(root, 0o755))
		home := filepath.Join(base, "home")
		tmp := filepath.Join(base, "tmp")
		mustDo(os.MkdirAll(home, 0o755))
		mustDo(os.MkdirAll(tmp, 0o755))
		if setup != nil {
			setup(t, root)
		}
		d := startWireDaemon(t, daemon.binary, root, daemonEnvironment(home, tmp))
		*daemon.out = body(d, root)
	}
	return pi, pig
}

func compareWire(t *testing.T, name, pi, pig string) {
	t.Helper()
	if pi == pig {
		return
	}
	piLines, pigLines := strings.Split(pi, "\n"), strings.Split(pig, "\n")
	for index := range max(len(piLines), len(pigLines)) {
		var a, b string
		if index < len(piLines) {
			a = piLines[index]
		}
		if index < len(pigLines) {
			b = pigLines[index]
		}
		if a != b {
			t.Fatalf("%s: line %d differs\nPi : %s\nPiG: %s", name, index+1, a, b)
		}
	}
}

// runWireCase runs one case against both daemons and compares every reply.
func runWireCase(t *testing.T, test wireCase) {
	t.Helper()
	if test.needsUser && os.Geteuid() == 0 {
		t.Skip("permissions do not stop root")
	}
	pi, pig := runOnBoth(t, test.setup, func(d *wireDaemon, root string) string {
		var text strings.Builder
		for index, step := range test.steps {
			request := withRoot(step.request, root).(daemon.Object)
			frames := d.call(request, step.payload)
			fmt.Fprintf(&text, "step %d %v\n%s", index, step.request["op"], render(frames, root, test.normalizers))
		}
		return text.String()
	})
	compareWire(t, test.name, pi, pig)
}

// fsTree builds the tree the file system cases share.
func fsTree(t *testing.T, root string) {
	t.Helper()
	write := func(name, content string, mode os.FileMode) {
		mustDo(os.WriteFile(filepath.Join(root, name), []byte(content), mode))
	}
	write("file.txt", "hello\nworld\n", 0o644)
	write("empty.txt", "", 0o644)
	write("notdir.txt", "x", 0o644)
	write("noperm.txt", "secret", 0o000)
	write("lines.txt", "\xef\xbb\xbfa\n\xe2\x82\n\nb\n\xc3\xa9", 0o644)
	mustDo(os.MkdirAll(filepath.Join(root, "dir", "sub"), 0o755))
	write("dir/a.txt", "A", 0o644)
	write("dir/b.txt", "BB", 0o644)
	write("dir/sub/c.txt", "CCC", 0o644)
	mustDo(os.MkdirAll(filepath.Join(root, "full", "inner"), 0o755))
	write("full/inner/x", "x", 0o644)
	write("full/y", "y", 0o644)
	mustDo(os.MkdirAll(filepath.Join(root, "locked"), 0o755))
	write("locked/f", "f", 0o644)
	mustDo(os.Chmod(filepath.Join(root, "locked"), 0o555))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "locked"), 0o755) })
	mustDo(os.Symlink("file.txt", filepath.Join(root, "link")))
	mustDo(os.Symlink("dir", filepath.Join(root, "dirlink")))
	mustDo(os.Symlink("missing-target", filepath.Join(root, "broken")))
	mustDo(os.Symlink("loop", filepath.Join(root, "loop")))
	mustDo(syscall.Mkfifo(filepath.Join(root, "fifo"), 0o644))
}

func object(pairs ...any) daemon.Object {
	result := daemon.Object{}
	for index := 0; index < len(pairs); index += 2 {
		result[pairs[index].(string)] = pairs[index+1]
	}
	return result
}

var mkdtempName = wireNormalizer{regexp.MustCompile(`(t-|"path":"\{ROOT\}/)[A-Za-z0-9]{6}`), `${1}XXXXXX`}

// fsCases are the file operations: Pi's fs.rs, errors.rs and sys/unix.rs answers to each.
func fsCases() []wireCase {
	root := func(name string) string { return "{ROOT}/" + name }
	long := strings.Repeat("n", 300)
	return []wireCase{
		{name: "lstat of every kind of path", setup: fsTree, steps: []wireStep{
			step(object("op", "lstat", "path", root("file.txt"))),
			step(object("op", "lstat", "path", root("dir"))),
			step(object("op", "lstat", "path", root("link"))),
			step(object("op", "lstat", "path", root("fifo"))),
			step(object("op", "lstat", "path", root("empty.txt"))),
			step(object("op", "lstat", "path", root("missing"))),
			step(object("op", "lstat", "path", root("notdir.txt/x"))),
			step(object("op", "lstat", "path", root("loop/x"))),
			step(object("op", "lstat", "path", root(long))),
			step(object("op", "lstat", "path", "")),
			step(object("op", "lstat", "path", root("broken"))),
			step(object("op", "lstat")),
			step(object("op", "lstat", "path", 7)),
		}},
		{name: "realpath", setup: fsTree, steps: []wireStep{
			step(object("op", "realpath", "path", root("link"))),
			step(object("op", "realpath", "path", root("dirlink/a.txt"))),
			step(object("op", "realpath", "path", root("dir/../file.txt"))),
			step(object("op", "realpath", "path", root("missing"))),
			step(object("op", "realpath", "path", root("loop"))),
			step(object("op", "realpath", "path", root("notdir.txt/x"))),
		}},
		{name: "mkdir", setup: fsTree, needsUser: true, steps: []wireStep{
			step(object("op", "mkdir", "path", root("new"))),
			step(object("op", "mkdir", "path", root("new"))),
			step(object("op", "mkdir", "path", root("new"), "recursive", true)),
			step(object("op", "mkdir", "path", root("a/b/c"), "recursive", true)),
			step(object("op", "mkdir", "path", root("a/b/d"))),
			step(object("op", "mkdir", "path", root("x/y"))),
			step(object("op", "mkdir", "path", root("file.txt"), "recursive", true)),
			step(object("op", "mkdir", "path", root("notdir.txt/p/q"), "recursive", true)),
			step(object("op", "mkdir", "path", root("locked/n"))),
			step(object("op", "mkdir", "path", root("locked/n/m"), "recursive", true)),
			step(object("op", "mkdir", "path", root("link/x"), "recursive", true)),
			step(object("op", "lstat", "path", root("a/b/c"))),
		}},
		{name: "write, append and chunked writes", setup: fsTree, needsUser: true, steps: []wireStep{
			{request: object("op", "write", "path", root("w/deep/new.txt")), payload: []byte("first")},
			{request: object("op", "write", "path", root("w/deep/new.txt"), "append", true), payload: []byte("+second")},
			{request: object("op", "write", "path", root("w2/x.txt"), "parents", false), payload: []byte("no parent")},
			{request: object("op", "write", "path", root("notdir.txt/a/b.txt")), payload: []byte("p")},
			{request: object("op", "write", "path", root("dir")), payload: []byte("onto a directory")},
			{request: object("op", "write", "path", root("locked/g")), payload: []byte("denied")},
			{request: object("op", "write", "path", root("kept.txt"), "keep", true), payload: []byte("a")},
			{request: object("op", "writeChunk", "handle", 1), payload: []byte("b")},
			{request: object("op", "writeChunk", "handle", 1), payload: []byte("c")},
			step(object("op", "writeChunk", "handle", 99)),
			step(object("op", "close", "handle", 1)),
			step(object("op", "writeChunk", "handle", 1)),
			{request: object("op", "write", "path", root("kept.txt"), "append", true, "keep", true), payload: []byte("d")},
			step(object("op", "open", "path", root("kept.txt"))),
			step(object("op", "pread", "handle", 3, "offset", 0, "length", 100)),
			step(object("op", "pread", "handle", 2, "offset", 0, "length", 100)),
			{request: object("op", "write", "path", root("w/deep/new.txt")), payload: []byte("")},
			step(object("op", "open", "path", root("w/deep/new.txt"))),
			step(object("op", "pread", "handle", 5, "offset", 0, "length", 100)),
			step(object("op", "write", "path", root("nopayload.txt"))),
			step(object("op", "lstat", "path", root("nopayload.txt"))),
		}},
		{name: "truncate and fsync", setup: fsTree, needsUser: true, steps: []wireStep{
			step(object("op", "truncate", "path", root("file.txt"), "size", 3)),
			step(object("op", "lstat", "path", root("file.txt"))),
			step(object("op", "truncate", "path", root("file.txt"), "size", 20)),
			step(object("op", "lstat", "path", root("file.txt"))),
			step(object("op", "truncate", "path", root("missing"), "size", 1)),
			step(object("op", "truncate", "path", root("dir"), "size", 1)),
			step(object("op", "truncate", "path", root("file.txt"))),
			step(object("op", "truncate", "path", root("file.txt"), "size", -1)),
			step(object("op", "truncate", "path", root("noperm.txt"), "size", 1)),
			step(object("op", "fsync", "path", root("file.txt"))),
			step(object("op", "fsync", "path", root("dir"))),
			step(object("op", "fsync", "path", root("missing"))),
			step(object("op", "fsync", "path", root("noperm.txt"))),
		}},
		{name: "rename", setup: fsTree, needsUser: true, steps: []wireStep{
			step(object("op", "rename", "path", root("file.txt"), "to", root("moved.txt"))),
			step(object("op", "rename", "path", root("missing"), "to", root("x"))),
			step(object("op", "rename", "path", root("dir"), "to", root("full"))),
			step(object("op", "rename", "path", root("moved.txt"), "to", root("dir"))),
			step(object("op", "rename", "path", root("dir"), "to", root("empty.txt"))),
			step(object("op", "rename", "path", root("empty.txt"), "to", root("nodir/x"))),
			step(object("op", "rename", "path", root("dir"), "to", root("dir/sub/inside"))),
			step(object("op", "rename", "path", root("locked/f"), "to", root("locked/g"))),
			step(object("op", "rename", "path", root("empty.txt"))),
			step(object("op", "rename", "path", root("empty.txt"), "to", root("empty.txt"))),
		}},
		{name: "rm", setup: fsTree, needsUser: true, steps: []wireStep{
			step(object("op", "rm", "path", root("empty.txt"))),
			step(object("op", "rm", "path", root("empty.txt"))),
			step(object("op", "rm", "path", root("empty.txt"), "force", true)),
			step(object("op", "rm", "path", root("full"))),
			step(object("op", "rm", "path", root("full"), "recursive", true)),
			step(object("op", "rm", "path", root("link"))),
			step(object("op", "lstat", "path", root("file.txt"))),
			step(object("op", "rm", "path", root("fifo"))),
			step(object("op", "rm", "path", root("dirlink"), "recursive", true)),
			step(object("op", "lstat", "path", root("dir"))),
			step(object("op", "rm", "path", root("locked/f"))),
			step(object("op", "rm", "path", root("locked"), "recursive", true)),
			step(object("op", "rm", "path", root("notdir.txt/x"), "force", true)),
			step(object("op", "rm", "path", root("notdir.txt/x"))),
			step(object("op", "rm", "path", root("loop/x"), "force", true)),
		}},
		{name: "mkdtemp", setup: fsTree, normalizers: []wireNormalizer{mkdtempName}, needsUser: true, steps: []wireStep{
			step(object("op", "mkdtemp", "path", root("t-"))),
			step(object("op", "mkdtemp", "path", root("nodir/t-"))),
			step(object("op", "mkdtemp", "path", root("locked/t-"))),
			step(object("op", "mkdtemp", "path", root(""))),
			step(object("op", "mkdtemp")),
		}},
		{name: "open for reading and reading", setup: fsTree, needsUser: true, steps: []wireStep{
			step(object("op", "open", "path", root("file.txt"))),
			step(object("op", "open", "path", root("dir"))),
			step(object("op", "open", "path", root("fifo"))),
			step(object("op", "open", "path", root("link"))),
			step(object("op", "open", "path", root("link"), "noFollow", true)),
			step(object("op", "open", "path", root("dirlink"), "noFollow", true)),
			step(object("op", "open", "path", root("broken"), "noFollow", true)),
			step(object("op", "open", "path", root("loop"))),
			step(object("op", "open", "path", root("loop"), "noFollow", true)),
			step(object("op", "open", "path", root("missing"))),
			step(object("op", "open", "path", root("noperm.txt"))),
			step(object("op", "open", "path", root("notdir.txt/x"))),
			step(object("op", "open", "path", root("empty.txt"), "mode", "read")),
			step(object("op", "open", "path", root("dir"), "mode", "read")),
			step(object("op", "open", "path", root("missing"), "mode", "read")),
			step(object("op", "open", "path", root("noperm.txt"), "mode", "read")),
			step(object("op", "open", "path", root("dirlink"), "mode", "read")),
			step(object("op", "open", "path", root("link"), "mode", "write")),
			step(object("op", "fstat", "handle", 1)),
			step(object("op", "fstat", "handle", 13)),
			step(object("op", "fstat", "handle", 99)),
			step(object("op", "fstat")),
			step(object("op", "pread", "handle", 1, "offset", 0, "length", 5)),
			step(object("op", "pread", "handle", 1, "offset", 6, "length", 100)),
			step(object("op", "pread", "handle", 1, "offset", 100, "length", 10)),
			step(object("op", "pread", "handle", 1, "offset", 0, "length", 0)),
			step(object("op", "pread", "handle", 1, "length", 4)),
			step(object("op", "pread", "handle", 1, "length", 4)),
			step(object("op", "pread", "handle", 1, "length", 400)),
			step(object("op", "pread", "handle", 1, "length", 400)),
			step(object("op", "pread", "handle", 1, "offset", 0)),
			step(object("op", "pread", "handle", 1, "offset", 0, "length", 1e12)),
			step(object("op", "pread", "handle", 1, "offset", 0, "length", -1)),
			step(object("op", "pread", "handle", 13, "offset", 0, "length", 10)),
			step(object("op", "pread", "handle", 99, "offset", 0, "length", 10)),
			step(object("op", "pread", "handle", "1", "offset", 0, "length", 10)),
			step(object("op", "close", "handle", 1)),
			step(object("op", "pread", "handle", 1, "offset", 0, "length", 10)),
			step(object("op", "close", "handle", 1)),
			step(object("op", "close")),
		}},
		{name: "scanLines", setup: fsTree, steps: []wireStep{
			step(object("op", "open", "path", root("lines.txt"))),
			step(object("op", "scanLines", "handle", 1, "startLine", 0)),
			step(object("op", "scanLines", "handle", 1, "startLine", 1, "endLine", 3)),
			step(object("op", "scanLines", "handle", 1, "startLine", 9)),
			step(object("op", "scanLines", "handle", 1, "startLine", 3, "endLine", 3)),
			step(object("op", "scanLines", "handle", 1, "startLine", 3, "endLine", 2)),
			step(object("op", "scanLines", "handle", 1)),
			step(object("op", "scanLines", "handle", 1, "startLine", 0, "endLine", 1)),
			step(object("op", "scanLines", "handle", 7, "startLine", 0)),
			step(object("op", "open", "path", root("empty.txt"))),
			step(object("op", "scanLines", "handle", 2, "startLine", 0)),
		}},
		{name: "directories", setup: fsTree, steps: []wireStep{
			step(object("op", "opendir", "path", root("dir"))),
			step(object("op", "readdir", "handle", 1, "max", 1)),
			step(object("op", "readdir", "handle", 1, "max", 0)),
			step(object("op", "readdir", "handle", 1, "max", 10)),
			step(object("op", "readdir", "handle", 1, "max", 10)),
			step(object("op", "opendir", "path", root("missing"))),
			step(object("op", "opendir", "path", root("file.txt"))),
			step(object("op", "opendir", "path", root("noperm.txt"))),
			step(object("op", "opendir", "path", root("dirlink"))),
			step(object("op", "readdir", "handle", 4, "max", 10)),
			step(object("op", "readdir", "handle", 99, "max", 10)),
			step(object("op", "readdir", "handle", 1)),
			step(object("op", "open", "path", root("file.txt"))),
			step(object("op", "readdir", "handle", 5, "max", 10)),
			step(object("op", "pread", "handle", 1, "offset", 0, "length", 1)),
			step(object("op", "close", "handle", 1)),
			step(object("op", "readdir", "handle", 1, "max", 10)),
		}},
		{name: "dispatch errors", setup: fsTree, steps: []wireStep{
			step(object("op", "frobnicate")),
			step(object()),
			step(object("op", 5)),
			step(object("op", "lstat", "path", nil)),
		}},
	}
}

func TestDaemonFileOperationsMatchPisDaemon(t *testing.T) {
	for _, test := range fsCases() {
		t.Run(test.name, func(t *testing.T) { runWireCase(t, test) })
	}
}

// TestDaemonNameEncodingsMatchPisDaemon lists a directory whose names are not UTF-8, as Node decodes them.
func TestDaemonNameEncodingsMatchPisDaemon(t *testing.T) {
	setup := func(t *testing.T, root string) {
		if err := os.WriteFile(filepath.Join(root, "bad\xffname"), []byte("x"), 0o644); err != nil {
			t.Skipf("this file system refuses names that are not UTF-8: %v", err)
		}
		mustDo(os.WriteFile(filepath.Join(root, "cut\xe2\x82"), nil, 0o644))
		mustDo(os.WriteFile(filepath.Join(root, "é.txt"), nil, 0o644))
		mustDo(os.WriteFile(filepath.Join(root, "plain"), nil, 0o644))
	}
	runWireCase(t, wireCase{name: "names", setup: setup, steps: []wireStep{
		step(object("op", "opendir", "path", "{ROOT}")),
		step(object("op", "readdir", "handle", 1, "max", 100)),
	}})
}

// TestDaemonHandlesAreLimitedLikePisDaemon opens handles until the limit and past it.
func TestDaemonHandlesAreLimitedLikePisDaemon(t *testing.T) {
	steps := make([]wireStep, 0, 4100)
	for range 4097 {
		steps = append(steps, step(object("op", "opendir", "path", "{ROOT}/dir")))
	}
	steps = append(steps,
		step(object("op", "close", "handle", 7)),
		step(object("op", "opendir", "path", "{ROOT}/dir")),
		step(object("op", "opendir", "path", "{ROOT}/dir")),
	)
	pi, pig := runOnBoth(t, fsTree, func(d *wireDaemon, root string) string {
		var text strings.Builder
		for _, step := range steps {
			fmt.Fprint(&text, render(d.call(withRoot(step.request, root).(daemon.Object), nil), root, nil))
		}
		return text.String()
	})
	compareWire(t, "handle limit", pi, pig)
	if !strings.Contains(pi, "EMFILE: too many open files") {
		t.Fatalf("Pi's daemon never reached its handle limit:\n%s", pi[len(pi)-400:])
	}
}

// TestDaemonPoisonsAWriteHandleAfterAFailedChunkLikePisDaemon fills /dev/full, where every write fails.
func TestDaemonPoisonsAWriteHandleAfterAFailedChunkLikePisDaemon(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	runWireCase(t, wireCase{name: "poison", steps: []wireStep{
		step(object("op", "write", "path", "/dev/full", "keep", true, "parents", false)),
		{request: object("op", "writeChunk", "handle", 1), payload: []byte("data")},
		{request: object("op", "writeChunk", "handle", 1), payload: []byte("more")},
		step(object("op", "close", "handle", 1)),
	}})
}

// TestDaemonOpensASocketLikePisDaemon: libuv names no error for ENXIO, so the OS text stands in the message.
func TestDaemonOpensASocketLikePisDaemon(t *testing.T) {
	setup := func(t *testing.T, root string) {
		descriptor, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err != nil {
			t.Skip(err)
		}
		t.Cleanup(func() { _ = syscall.Close(descriptor) })
		if err := syscall.Bind(descriptor, &syscall.SockaddrUnix{Name: filepath.Join(root, "s")}); err != nil {
			t.Skipf("no socket here: %v", err)
		}
	}
	runWireCase(t, wireCase{name: "socket", setup: setup, steps: []wireStep{
		step(object("op", "open", "path", "{ROOT}/s")),
		step(object("op", "open", "path", "{ROOT}/s", "mode", "read")),
		step(object("op", "lstat", "path", "{ROOT}/s")),
		step(object("op", "truncate", "path", "{ROOT}/s", "size", 1)),
	}})
}

// TestDaemonAnswersAFrameWhoseJSONDoesNotParseLikePisDaemon sends bytes that are no JSON inside a well-formed frame.
func TestDaemonAnswersAFrameWhoseJSONDoesNotParseLikePisDaemon(t *testing.T) {
	pi, pig := runOnBoth(t, nil, func(d *wireDaemon, root string) string {
		var text strings.Builder
		for _, raw := range []string{"{{{", "", "null", "[]", `"x"`, "7", `{"op":"lstat","path":"/nonexistent-pi-env"} trailing`} {
			id := d.nextID
			d.nextID++
			frame := make([]byte, 0, 32)
			length := 9 + len(raw)
			frame = append(frame, byte(length>>24), byte(length>>16), byte(length>>8), byte(length), daemon.FrameRequest,
				byte(id>>24), byte(id>>16), byte(id>>8), byte(id), byte(len(raw)>>24), byte(len(raw)>>16), byte(len(raw)>>8), byte(len(raw)))
			frame = append(frame, raw...)
			if _, err := d.in.Write(frame); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&text, "%q\n%s", raw, render(d.settle(id), root, nil))
		}
		return text.String()
	})
	compareWire(t, "unparseable JSON", pi, pig)
}

// TestDaemonEndsOnAFramingErrorLikePisDaemon: both daemons stop at once, with nothing sent for the bad frame.
func TestDaemonEndsOnAFramingErrorLikePisDaemon(t *testing.T) {
	for _, length := range [][]byte{{0, 0, 0, 8}, {0x01, 0, 0, 1}, {0xff, 0xff, 0xff, 0xff}} {
		var outcomes [2]string
		oracle := oracleDaemon(t)
		for index, binary := range []string{oracle, daemonBinary(t)} {
			base := t.TempDir()
			d := startWireDaemon(t, binary, base, daemonEnvironment(base, base))
			_, _ = d.in.Write(append(append([]byte(nil), length...), 1, 0, 0, 0, 1, 0, 0, 0, 0))
			_ = d.out.SetReadDeadline(time.Now().Add(20 * time.Second))
			rest, err := io.ReadAll(d.reader)
			outcomes[index] = fmt.Sprintf("output %q error %v", bytes.TrimSpace(rest), err)
			waited := make(chan error, 1)
			go func() { waited <- d.cmd.Wait() }()
			select {
			case exit := <-waited:
				outcomes[index] += fmt.Sprintf(" exit %v", exit)
			case <-time.After(20 * time.Second):
				t.Fatalf("%s did not end", binary)
			}
		}
		if outcomes[0] != outcomes[1] {
			t.Fatalf("length bytes %v:\nPi : %s\nPiG: %s", length, outcomes[0], outcomes[1])
		}
	}
}

// TestDaemonStatReportsTheFilesIdentityLikePisDaemon asks both daemons about the same files, so the times, device and inode in
// the replies are compared as they are, not replaced by placeholders.
func TestDaemonStatReportsTheFilesIdentityLikePisDaemon(t *testing.T) {
	oracle := oracleDaemon(t)
	base := t.TempDir()
	root := filepath.Join(base, "root")
	mustDo(os.MkdirAll(root, 0o755))
	fsTree(t, root)
	moment := time.Unix(1_700_000_123, 456_789_012)
	for _, name := range []string{"file.txt", "dir", "empty.txt"} {
		mustDo(os.Chtimes(filepath.Join(root, name), moment, moment.Add(time.Duration(len(name))*time.Second)))
	}
	steps := []daemon.Object{
		object("op", "lstat", "path", filepath.Join(root, "file.txt")),
		object("op", "lstat", "path", filepath.Join(root, "dir")),
		object("op", "lstat", "path", filepath.Join(root, "link")),
		object("op", "lstat", "path", filepath.Join(root, "fifo")),
		object("op", "open", "path", filepath.Join(root, "file.txt")),
		object("op", "fstat", "handle", 1),
		object("op", "open", "path", filepath.Join(root, "dir"), "mode", "read"),
		object("op", "open", "path", filepath.Join(root, "link"), "mode", "read"),
		object("op", "opendir", "path", filepath.Join(root, "dir")),
		object("op", "readdir", "handle", 4, "max", 10),
		object("op", "opendir", "path", root),
		object("op", "readdir", "handle", 5, "max", 100),
	}
	var outputs [2]string
	for index, binary := range []string{oracle, daemonBinary(t)} {
		d := startWireDaemon(t, binary, root, daemonEnvironment(base, base))
		var text strings.Builder
		for _, request := range steps {
			text.WriteString(renderWith(d.call(request, nil), root, nil))
		}
		outputs[index] = text.String()
	}
	compareWire(t, "identity", outputs[0], outputs[1])
	if !strings.Contains(outputs[0], `"mtimeNsec":456789012`) {
		t.Fatalf("Pi's daemon did not report the file's modification time:\n%s", outputs[0])
	}
}
