package cli

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// halfCloseConn ends a connection's write side on Close, as a client's stdin end: the server still flushes its records to the open read side.
type halfCloseConn struct{ *net.UnixConn }

func (c halfCloseConn) Close() error { return c.CloseWrite() }

// listenTempDir is a short-path directory for a socket: the grouped test runner's scoped TMPDIR pushes a socket path past the 104-byte sun_path of macOS, so Unix hosts root it at /tmp.
func listenTempDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return shortTempDir(t)
	}
	//portlint:allow hardtmp a socket path must stay under the 104-byte sun_path of macOS, which a scoped TMPDIR exceeds
	dir, err := os.MkdirTemp("/tmp", "pig-listen-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove %s: %v", dir, err)
		}
	})
	return dir
}

// listenServer is one `pig --mode rpc --listen` child.
type listenServer struct {
	t      *testing.T
	cmd    *exec.Cmd
	path   string
	errBuf *lockedBuffer
	exited chan struct{}
	code   int
	budget time.Duration
}

func listenEnv(t *testing.T, dir string) []string {
	t.Helper()
	return []string{
		"PIG_TEST_FAUX=1",
		"PIG_HOME=" + filepath.Join(dir, "home"),
		"PI_HOME=" + filepath.Join(dir, "pihome"),
		"PIG_CODING_AGENT_DIR=" + filepath.Join(dir, "agent"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(dir, "piagent"),
	}
}

// startListenServer starts the server on dir/pig.sock unless args already carry --listen.
func startListenServer(t *testing.T, env []string, args ...string) *listenServer {
	t.Helper()
	s := &listenServer{t: t, errBuf: &lockedBuffer{}, exited: make(chan struct{}), budget: testbudget.Wait(t)}
	cmd := exec.Command(buildPigBinaryForSignalTest(t), args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = s.errBuf
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.cmd = cmd
	go func() {
		err := cmd.Wait()
		s.code = cmd.ProcessState.ExitCode()
		if err != nil && s.code == -1 {
			s.code = -1
		}
		close(s.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-s.exited:
		default:
			_ = cmd.Process.Kill()
			<-s.exited
		}
	})
	return s
}

func newListenServer(t *testing.T, extra ...string) *listenServer {
	t.Helper()
	return newListenServerIn(t, listenTempDir(t), extra...)
}

// newListenServerIn runs the server over dir, whose path the system prompt carries: a comparison against another process uses one dir for both.
func newListenServerIn(t *testing.T, dir string, extra ...string) *listenServer {
	t.Helper()
	path := filepath.Join(dir, "pig.sock")
	args := append([]string{"--mode", "rpc", "--listen", path, "--model", "test-faux/echo", "--no-session"}, extra...)
	s := startListenServer(t, listenEnv(t, dir), args...)
	s.path = path
	return s
}

func (s *listenServer) stderr() string { return s.errBuf.String() }

// dial connects and returns the session's client.
func (s *listenServer) dial() *rpcProcess {
	s.t.Helper()
	uc := s.dialConn()
	p := newRPCClient(s.t, halfCloseConn{uc}, uc)
	p.stderr = s.errBuf
	s.t.Cleanup(func() {
		close(p.stopOutput)
		_ = uc.Close()
		<-p.outputDone
	})
	return p
}

// dialConn connects, retrying until the server listens.
func (s *listenServer) dialConn() *net.UnixConn {
	s.t.Helper()
	deadline := time.Now().Add(s.budget)
	for {
		conn, err := net.Dial("unix", s.path)
		if err == nil {
			return conn.(*net.UnixConn)
		}
		select {
		case <-s.exited:
			s.t.Fatalf("server exited before accepting: %d\n%s", s.code, s.stderr())
		default:
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("server never listened: %v\n%s", err, s.stderr())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *listenServer) signal(sig os.Signal) {
	s.t.Helper()
	if runtime.GOOS == "windows" {
		s.t.Skip("a termination signal cannot be delivered to a child on windows")
	}
	if err := s.cmd.Process.Signal(sig); err != nil {
		s.t.Fatal(err)
	}
}

func (s *listenServer) waitForExit() int {
	s.t.Helper()
	timer := time.NewTimer(s.budget)
	defer timer.Stop()
	select {
	case <-s.exited:
		return s.code
	case <-timer.C:
		_ = s.cmd.Process.Kill()
		<-s.exited
		s.t.Fatalf("server did not exit within %s\n%s", s.budget, s.stderr())
		return 0
	}
}

func (s *listenServer) requireSocketGone() {
	s.t.Helper()
	if _, err := os.Lstat(s.path); !errors.Is(err, os.ErrNotExist) {
		s.t.Fatalf("socket %s still exists after exit: %v", s.path, err)
	}
}

// requestResponse sends a command and returns its response record.
func requestResponse(p *rpcProcess, id, command string) rpcRecord {
	p.t.Helper()
	p.sendJSON(map[string]any{"id": id, "type": command})
	var response rpcRecord
	p.await(command+" response", func(r rpcRecord) bool {
		if r["type"] == "response" && r["id"] == id {
			response = r
			return true
		}
		return false
	})
	return response
}

var (
	listenVolatileKeys = map[string]bool{
		"id": true, "parentId": true, "sessionId": true, "responseId": true, "timestamp": true,
		"sessionFile": true, "duration": true, "durationMs": true, "startTime": true, "endTime": true,
	}
	listenTempPath = regexp.MustCompile(`(?:/|[A-Za-z]:\\)[^"\s]*?/(?:pig-|go-build|T)[^"\s]*`)
)

// normalizeRPCRecord applies the parity runner's JSON alias rules by key name: ids, timestamps, durations and session files are scalar identities, and temp directory paths are roots (compareJSONResults in test/parity/runner is build-tagged and unexported, so its rules are copied here).
func normalizeRPCRecord(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if listenVolatileKeys[key] {
				if _, isString := item.(string); isString || key != "id" {
					out[key] = "<" + key + ">"
					continue
				}
			}
			out[key] = normalizeRPCRecord(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeRPCRecord(item)
		}
		return out
	case string:
		return listenTempPath.ReplaceAllString(v, "<path>")
	}
	return value
}

// runListenScript runs the equality script and returns every record the client saw, normalized.
func runListenScript(p *rpcProcess) []rpcRecord {
	p.t.Helper()
	var records []rpcRecord
	collect := func(done func(rpcRecord) bool) func(rpcRecord) bool {
		return func(r rpcRecord) bool {
			records = append(records, normalizeRPCRecord(r).(map[string]any))
			return done(r)
		}
	}
	response := func(id string) func(rpcRecord) bool {
		return func(r rpcRecord) bool { return r["type"] == "response" && r["id"] == id }
	}
	for _, step := range []struct{ id, command string }{{"1", "get_state"}, {"2", "get_available_models"}} {
		p.sendJSON(map[string]any{"id": step.id, "type": step.command})
		p.await(step.command, collect(response(step.id)))
	}
	p.sendJSON(map[string]any{"id": "3", "type": "prompt", "message": "hello"})
	p.await("agent_end", collect(func(r rpcRecord) bool { return r["type"] == "agent_end" }))
	for _, step := range []struct{ id, command string }{{"4", "get_messages"}, {"5", "get_session_stats"}} {
		p.sendJSON(map[string]any{"id": step.id, "type": step.command})
		p.await(step.command, collect(response(step.id)))
	}
	return records
}

// The second connection is built through listenBuild, not the startup build the first connection takes: its records equal pig --mode rpc's (D232).
func TestListenSecondConnectionMatchesPigModeRPC(t *testing.T) {
	dir := listenTempDir(t)
	env := listenEnv(t, dir)
	child := startRPCProcess(t, env, "--model", "test-faux/echo", "--no-session")
	want := runListenScript(child)
	child.closeAndWait("equality script")

	server := newListenServerIn(t, dir)
	first := server.dial()
	requestResponse(first, "ready", "get_state")
	conn := server.dial()
	got := runListenScript(conn)
	conn.closeInput()
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", " ")
		wantJSON, _ := json.MarshalIndent(want, "", " ")
		t.Fatalf("second connection's records differ from pig --mode rpc\n--- connection\n%s\n--- process\n%s", gotJSON, wantJSON)
	}
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
}

func TestListenServesOneRPCSessionPerConnection(t *testing.T) {
	dir := listenTempDir(t)
	env := listenEnv(t, dir)
	child := startRPCProcess(t, env, "--model", "test-faux/echo", "--no-session")
	want := runListenScript(child)
	child.closeAndWait("equality script")

	server := newListenServerIn(t, dir)
	conn := server.dial()
	got := runListenScript(conn)
	conn.closeInput()
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", " ")
		wantJSON, _ := json.MarshalIndent(want, "", " ")
		t.Fatalf("connection records differ from pig --mode rpc\n--- connection\n%s\n--- process\n%s", gotJSON, wantJSON)
	}
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
	server.requireSocketGone()
}

func TestListenSessionsRunConcurrently(t *testing.T) {
	server := newListenServer(t)
	prompts := []string{"alpha", "beta", "gamma"}
	conns := make([]*rpcProcess, len(prompts))
	for i := range conns {
		conns[i] = server.dial()
		requestResponse(conns[i], "ready", "get_state")
	}
	for i, prompt := range prompts {
		conns[i].sendJSON(map[string]any{"id": "prompt", "type": "prompt", "message": prompt})
	}
	for i, prompt := range prompts {
		p := conns[i]
		ends := 0
		count := func(r rpcRecord) {
			if r["type"] == "agent_end" {
				ends++
			}
		}
		p.await("agent_end of "+prompt, func(r rpcRecord) bool { count(r); return r["type"] == "agent_end" })
		p.sendJSON(map[string]any{"id": "messages", "type": "get_messages"})
		var messages rpcRecord
		p.await("get_messages of "+prompt, func(r rpcRecord) bool {
			count(r)
			if r["type"] == "response" && r["id"] == "messages" {
				messages = r
				return true
			}
			return false
		})
		encoded, _ := json.Marshal(messages["data"])
		for _, other := range prompts {
			if has := strings.Contains(string(encoded), `"text":"`+other+`"`); has != (other == prompt) {
				t.Fatalf("connection %q: messages mention %q = %v: %s", prompt, other, has, encoded)
			}
		}
		if ends != 1 {
			t.Fatalf("connection %q saw %d agent_end records, want 1", prompt, ends)
		}
	}
	// Overlap: connection 0 runs a three-second bash command; the others must answer while it runs.
	start := time.Now()
	conns[0].sendJSON(map[string]any{"id": "slow", "type": "bash", "command": "sleep 3"})
	for i, p := range conns[1:] {
		if state := requestResponse(p, "during", "get_state"); state["success"] != true {
			t.Fatalf("connection %d during the first one's bash: %v", i+1, state)
		}
	}
	if took := time.Since(start); took >= 3*time.Second {
		t.Fatalf("the other connections answered after %s, not while connection 0's bash ran", took)
	}
	conns[0].await("bash response", func(r rpcRecord) bool { return r["type"] == "response" && r["id"] == "slow" })
	if took := time.Since(start); took < 3*time.Second {
		t.Fatalf("bash finished after %s, before its sleep", took)
	}
	conns[0].closeInput()
	conns[0].waitForEOF()
	for i, p := range conns[1:] {
		if state := requestResponse(p, "after", "get_state"); state["success"] != true {
			t.Fatalf("connection %d after the first closed: %v", i+1, state)
		}
	}
	server.signal(syscall.SIGTERM)
	server.waitForExit()
}

func TestListenHostsForgetsAClosedConnectionsHost(t *testing.T) {
	hosts := &listenHosts{}
	a, b := &cliBuild{Host: &subprocess.Host{}}, &cliBuild{Host: &subprocess.Host{}}
	removeA := hosts.add(a)
	hosts.add(b)
	hosts.add(&cliBuild{})
	if got := hosts.len(); got != 2 {
		t.Fatalf("live hosts = %d, want 2", got)
	}
	removeA()
	if got := hosts.len(); got != 1 {
		t.Fatalf("live hosts after a connection ended = %d, want 1", got)
	}
	if _, held := hosts.m[a.Host]; held {
		t.Fatal("the ended connection's Host is still held")
	}
}

func TestListenASecondSIGTERMEndsAServerWhoseSessionDisposeHangs(t *testing.T) {
	dir := listenTempDir(t)
	report := filepath.Join(dir, "report")
	fixture, err := filepath.Abs(filepath.Join("testdata", "listen-hang-shutdown.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pig.sock")
	server := startListenServer(t, append(listenEnv(t, dir), "LISTEN_SHUTDOWN_REPORT="+report),
		"--mode", "rpc", "--listen", path, "--model", "test-faux/echo", "--no-session", "-e", fixture)
	server.path = path
	conn := server.dial()
	requestResponse(conn, "ready", "get_state")
	server.signal(syscall.SIGTERM)
	// The handler's report proves the first signal's dispose is the one hanging.
	deadline := time.Now().Add(server.budget)
	for {
		if data, _ := os.ReadFile(report); strings.Contains(string(data), "session_shutdown") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session_shutdown never reached the handler\n%s", server.stderr())
		}
		select {
		case <-server.exited:
			t.Fatalf("the server exited before the second signal: %d\n%s", server.code, server.stderr())
		case <-time.After(20 * time.Millisecond):
		}
	}
	select {
	case <-server.exited:
		t.Fatalf("the server exited with a hung dispose and one signal: %d", server.code)
	default:
	}
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
	server.requireSocketGone()
}

// waitForEOF consumes records until the server closes the connection.
func (p *rpcProcess) waitForEOF() {
	p.t.Helper()
	timer := time.NewTimer(p.budget)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-p.records:
			if !ok {
				return
			}
		case <-timer.C:
			p.t.Fatalf("connection stayed open after its input ended\n%s", p.stderr.String())
		}
	}
}

func TestListenExitsOnSIGTERMWithEveryConnectionOpen(t *testing.T) {
	server := newListenServer(t)
	conns := []*rpcProcess{server.dial(), server.dial()}
	for _, p := range conns {
		requestResponse(p, "ready", "get_state")
	}
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
	server.requireSocketGone()
	for _, p := range conns {
		p.waitForEOF()
	}
}

func TestListenRefusesAnExistingPath(t *testing.T) {
	dir := listenTempDir(t)
	path := filepath.Join(dir, "pig.sock")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := startListenServer(t, listenEnv(t, dir), "--mode", "rpc", "--listen", path, "--model", "test-faux/echo", "--no-session")
	if code := server.waitForExit(); code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, server.stderr())
	}
	if want := "pig --listen: " + path + " exists"; !strings.Contains(server.stderr(), want) {
		t.Fatalf("stderr %q does not contain %q", server.stderr(), want)
	}
	if data, _ := os.ReadFile(path); string(data) != "keep" {
		t.Fatalf("the existing path was modified: %q", data)
	}
}

func TestListenRefusesAPathTheOSRejects(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, strings.Repeat("a", 200))
	server := startListenServer(t, listenEnv(t, dir), "--mode", "rpc", "--listen", path, "--model", "test-faux/echo", "--no-session")
	if code := server.waitForExit(); code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, server.stderr())
	}
	if want := "pig --listen: listen " + path + ":"; !strings.Contains(server.stderr(), want) {
		t.Fatalf("stderr %q does not contain %q", server.stderr(), want)
	}
}

func TestListenServesAfterAConnectionClosedBeforeItsSessionStarted(t *testing.T) {
	server := newListenServer(t)
	// The first connection takes the startup build; the second builds anew and closes before it is served: the server logs nothing fatal and the next connection is served normally.
	first := server.dial()
	requestResponse(first, "ready", "get_state")
	conn, err := net.Dial("unix", server.path)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	normal := server.dial()
	if state := requestResponse(normal, "state", "get_state"); state["success"] != true {
		t.Fatalf("get_state after a closed connection: %v", state)
	}
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
	server.requireSocketGone()
}

// waitForFile polls until path exists.
func (s *listenServer) waitForFile(path, what string) {
	s.t.Helper()
	deadline := time.Now().Add(s.budget)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("%s: %s never appeared\n%s", what, path, s.stderr())
		}
		select {
		case <-s.exited:
			s.t.Fatalf("%s: the server exited (%d) before %s appeared\n%s", what, s.code, path, s.stderr())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// The second connection's build runs an extension factory that never returns; its marker proves the build is in flight when the signal lands. The shutdown cancels the build, so the first signal ends the server.
func TestListenExitsOnSIGTERMDuringABuild(t *testing.T) {
	dir := listenTempDir(t)
	marker := filepath.Join(dir, "building")
	fixture, err := filepath.Abs(filepath.Join("testdata", "listen-hang-build.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pig.sock")
	server := startListenServer(t, append(listenEnv(t, dir), "LISTEN_BUILD_COUNT="+filepath.Join(dir, "count"), "LISTEN_BUILD_MARKER="+marker),
		"--mode", "rpc", "--listen", path, "--model", "test-faux/echo", "--no-session", "-e", fixture)
	server.path = path
	first := server.dial()
	requestResponse(first, "ready", "get_state")
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	server.waitForFile(marker, "the second connection's build")
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
	server.requireSocketGone()
}

// Ctrl+C is a termination signal under --listen: the sessions shut down and the socket file is removed.
func TestListenExitsOnSIGINT(t *testing.T) {
	server := newListenServer(t)
	conn := server.dial()
	requestResponse(conn, "ready", "get_state")
	server.signal(syscall.SIGINT)
	conn.waitForEOF()
	if code := server.waitForExit(); code != 128+int(syscall.SIGINT) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGINT), server.stderr())
	}
	server.requireSocketGone()
}

// flood sends count get_available_models commands, each answered by a response of several hundred bytes, then a bash command that creates marker, and returns once marker exists: the server has dispatched every command, and its responses wait in the connection's tail for a peer that is not reading. marker is created through the shell, so the flood needs a POSIX shell.
func (s *listenServer) flood(conn net.Conn, count int, marker string) {
	s.t.Helper()
	var batch strings.Builder
	for i := range count {
		batch.WriteString(`{"id":"r` + strconv.Itoa(i) + `","type":"get_available_models"}` + "\n")
	}
	markerCommand, err := json.Marshal(map[string]string{"id": "marker", "type": "bash", "command": ": > '" + filepath.ToSlash(marker) + "'"})
	if err != nil {
		s.t.Fatal(err)
	}
	batch.Write(markerCommand)
	batch.WriteString("\n")
	written := make(chan error, 1)
	go func() {
		_, err := conn.Write([]byte(batch.String()))
		written <- err
	}()
	deadline := time.Now().Add(s.budget)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("the server never dispatched the flood\n%s", s.stderr())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := <-written; err != nil {
		s.t.Fatal(err)
	}
}

func TestListenSIGTERMFlushesEveryConnectionsPendingRecords(t *testing.T) {
	server := newListenServer(t)
	other := server.dial()
	requestResponse(other, "ready", "get_state")
	conn := server.dialConn()
	t.Cleanup(func() { _ = conn.Close() })
	const count = 4000
	server.flood(conn, count, filepath.Join(filepath.Dir(server.path), "marker"))
	// The peer is not reading, so most of the records wait in the connection's tail when the signal arrives.
	server.signal(syscall.SIGTERM)
	seen := 0
	err := readJSONLRecords(conn, func(r rpcRecord) {
		if id, _ := r["id"].(string); strings.HasPrefix(id, "r") && r["type"] == "response" {
			if want := "r" + strconv.Itoa(seen); id != want {
				t.Fatalf("response %d is %q, want %q", seen, id, want)
			}
			seen++
		}
	})
	if err != nil {
		t.Fatalf("reading the flushed records: %v", err)
	}
	if seen != count {
		t.Fatalf("received %d of %d responses before the connection closed", seen, count)
	}
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
	server.requireSocketGone()
	if strings.Contains(server.stderr(), "closed") {
		t.Fatalf("the reader reported its closed connection:\n%s", server.stderr())
	}
}

func TestListenAPeerThatStopsReadingDoesNotWedgeTheServer(t *testing.T) {
	server := newListenServer(t)
	conn := server.dialConn()
	t.Cleanup(func() { _ = conn.Close() })
	server.flood(conn, 4000, filepath.Join(filepath.Dir(server.path), "marker"))
	other := server.dial()
	if state := requestResponse(other, "state", "get_state"); state["success"] != true {
		t.Fatalf("a second connection beside the stalled one: %v", state)
	}
	start := time.Now()
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
	if took := time.Since(start); took > connectionFlushTimeout+connectionFlushTimeout {
		t.Fatalf("the stalled peer held the exit for %s", took)
	}
	server.requireSocketGone()
}

func TestListenASecondSIGTERMClosesAStalledConnectionAtOnce(t *testing.T) {
	server := newListenServer(t)
	conn := server.dialConn()
	t.Cleanup(func() { _ = conn.Close() })
	server.flood(conn, 4000, filepath.Join(filepath.Dir(server.path), "marker"))
	start := time.Now()
	server.signal(syscall.SIGTERM)
	// A termination signal that arrives once the session's shutdown started is rpcShutdown.forceDie, which ends the connection with the signal's exit code instead of waiting for the peer.
	for {
		select {
		case <-server.exited:
			if code := server.code; code != 128+int(syscall.SIGTERM) {
				t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
			}
			if took := time.Since(start); took >= connectionFlushTimeout {
				t.Fatalf("the second signal did not close the stalled connection: exit after %s", took)
			}
			server.requireSocketGone()
			return
		case <-time.After(50 * time.Millisecond):
			_ = server.cmd.Process.Signal(syscall.SIGTERM)
		}
	}
}

// readJSONLRecords reads conn to its end and calls fn with each record.
func readJSONLRecords(conn net.Conn, fn func(rpcRecord)) error {
	return rpcclient.ReadJSONLLines(conn, func(line []byte) bool {
		var record rpcRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return false
		}
		fn(record)
		return true
	})
}
