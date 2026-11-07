package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// listenProcess is one descendant of the server that cellProcessCount matched.
type listenProcess struct {
	pid     int
	command string
}

// cellProcesses lists the descendants of serverPID that run the Node runtime and the Go cell binary.
func cellProcesses(t *testing.T, serverPID int) (nodeCells, goCells []listenProcess) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the process list is read with ps")
	}
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output()
	if err != nil {
		t.Fatal(err)
	}
	parent := map[int]int{}
	command := map[int]string{}
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		ppid, _ := strconv.Atoi(fields[1])
		parent[pid] = ppid
		command[pid] = strings.Join(fields[2:], " ")
	}
	pids := make([]int, 0, len(parent))
	for pid := range parent {
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	for _, pid := range pids {
		for p, hops := parent[pid], 0; p > 1 && hops < 64; p, hops = parent[p], hops+1 {
			if p != serverPID {
				continue
			}
			match := listenProcess{pid, command[pid]}
			executable := strings.Fields(command[pid])[0]
			switch {
			case filepath.Base(executable) == "node":
				nodeCells = append(nodeCells, match)
			case filepath.Base(executable) == "runner" && strings.Contains(filepath.ToSlash(executable), "/cells/go/"):
				goCells = append(goCells, match)
			}
			break
		}
	}
	return nodeCells, goCells
}

// cellProcessCount is the number of Node runtime and Go cell processes below the server. A failing assertion reads the matches from cellProcessReport.
func cellProcessCount(t *testing.T, serverPID int) (nodeCells, goCells int) {
	t.Helper()
	node, goc := cellProcesses(t, serverPID)
	return len(node), len(goc)
}

func cellProcessReport(t *testing.T, serverPID int) string {
	t.Helper()
	node, goc := cellProcesses(t, serverPID)
	var b strings.Builder
	for _, p := range node {
		fmt.Fprintf(&b, "node %d: %s\n", p.pid, p.command)
	}
	for _, p := range goc {
		fmt.Fprintf(&b, "go %d: %s\n", p.pid, p.command)
	}
	return b.String()
}

// requireCellCounts waits until the server has exactly the wanted cell processes.
func requireCellCounts(t *testing.T, s *listenServer, wantNode, wantGo int, what string) {
	t.Helper()
	deadline := time.Now().Add(s.budget)
	for {
		node, goc := cellProcessCount(t, s.cmd.Process.Pid)
		if node == wantNode && goc == wantGo {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: (nodeCells, goCells) = (%d, %d), want (%d, %d)\n%s\n%s", what, node, goc, wantNode, wantGo, cellProcessReport(t, s.cmd.Process.Pid), s.stderr())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// installListenGoExtension copies the Go counter fixture into the extensions directory of an agent directory, with its SDK replacement pointing at the repository's.
func installListenGoExtension(t *testing.T, extensions string) {
	t.Helper()
	goDir := filepath.Join(extensions, "listen-counter-go")
	if err := os.MkdirAll(goDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sdkDir, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		from, to string
		edit     func(string) string
	}{
		{filepath.Join("testdata", "listen-counter-go", "extension.go"), filepath.Join(goDir, "extension.go"), func(s string) string { return s }},
		{filepath.Join("testdata", "listen-counter-go", "go.mod"), filepath.Join(goDir, "go.mod"), func(s string) string {
			return strings.Replace(s, "../../../../extensions/sdk", filepath.ToSlash(sdkDir), 1)
		}},
	} {
		data, err := os.ReadFile(f.from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.to, []byte(f.edit(string(data))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// shareExtensions is the opt-in that makes a --listen server's sessions share extension processes (D232).
const shareExtensions = "--listen-share-extensions"

// listenExtensionServer starts a server whose agent directory holds the Go and the Node counter fixtures, and returns it with the file the fixtures' session_shutdown handlers append to. extra are further server arguments, such as shareExtensions.
func listenExtensionServer(t *testing.T, extra ...string) (*listenServer, string) {
	t.Helper()
	return listenExtensionServerBinary(t, listenTestBinary(t), extra...)
}

// listenExtensionServerBinary is listenExtensionServer over the pig executable at binary.
func listenExtensionServerBinary(t *testing.T, binary string, extra ...string) (*listenServer, string) {
	t.Helper()
	dir := listenTempDir(t)
	extensions := filepath.Join(dir, "agent", "extensions")
	nodeDir := filepath.Join(extensions, "listen-counter")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	installListenGoExtension(t, extensions)
	data, err := os.ReadFile(filepath.Join("testdata", "listen-counter.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "index.js"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "shutdown.log")
	path := filepath.Join(dir, "pig.sock")
	env := append(listenEnv(t, dir), "LISTEN_FIXTURE_LOG="+log, "PIG_TEST_FAUX_SCENARIO=parity-basic")
	s := startListenServerBinary(t, binary, env, append([]string{"--mode", "rpc", "--listen", path, "--model", "test-faux/faux-1", "--no-session"}, extra...)...)
	s.path = path
	return s, log
}

// callTool runs the tool in the session through the faux model and returns the tool result's text.
func callTool(p *rpcProcess, tool string) string {
	p.t.Helper()
	p.sendJSON(map[string]any{"id": "call-" + tool, "type": "prompt", "message": "Run: ext tool " + tool})
	var result string
	p.await(tool+" result", func(r rpcRecord) bool {
		if r["type"] != "tool_execution_end" || r["toolName"] != tool {
			return false
		}
		result = toolResultText(r["result"])
		if r["isError"] == true {
			result = "error: " + result
		}
		return true
	})
	p.await("agent_end after "+tool, func(r rpcRecord) bool { return r["type"] == "agent_end" })
	return result
}

func toolResultText(result any) string {
	m, _ := result.(map[string]any)
	content, _ := m["content"].([]any)
	var parts []string
	for _, c := range content {
		if block, ok := c.(map[string]any); ok {
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "")
}

func readShutdownLog(log string) []string {
	data, _ := os.ReadFile(log)
	return strings.Fields(string(data))
}

func requireShutdownLines(t *testing.T, s *listenServer, log string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(s.budget)
	for {
		lines := readShutdownLog(log)
		if len(lines) >= want {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d session_shutdown lines, want %d: %q\n%s", len(lines), want, lines, s.stderr())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// With --listen-share-extensions the sessions run in one process per cell and share its module-level state (D232).
func TestListenSharesExtensionProcessesAcrossSessions(t *testing.T) {
	s, log := listenExtensionServer(t, shareExtensions)
	conns := make([]*rpcProcess, 3)
	for i := range conns {
		conns[i] = s.dial()
		for _, tool := range []string{"node_counter", "go_counter"} {
			if got := callTool(conns[i], tool); got != "1" {
				t.Fatalf("connection %d %s = %q, want 1\n%s", i+1, tool, got, s.stderr())
			}
		}
		requireCellCounts(t, s, 1, 1, fmt.Sprintf("after connection %d", i+1))
		// Module-level state lives in the shared process, so the sessions count together (the hazard the opt-in's warning names).
		for _, tool := range []string{"node_module_counter", "go_module_counter"} {
			if got := callTool(conns[i], tool); got != strconv.Itoa(i+1) {
				t.Fatalf("connection %d %s = %q, want %d: module-level state is shared with --listen-share-extensions\n%s", i+1, tool, got, i+1, s.stderr())
			}
		}
	}
	// A ping from the first session reaches that session's instances only.
	for _, tool := range []string{"node_ping", "go_ping"} {
		if got := callTool(conns[0], tool); got != "pinged" {
			t.Fatalf("%s = %q", tool, got)
		}
	}
	for i, conn := range conns {
		for _, tool := range []string{"node_received", "go_received"} {
			got := callTool(conn, tool)
			if i == 0 {
				if got != "received=node,go" && got != "received=go,node" {
					t.Fatalf("connection 1 %s = %q, want both pings", tool, got)
				}
			} else if got != "received=" {
				t.Fatalf("connection %d %s = %q, want nothing", i+1, tool, got)
			}
		}
	}
	if lines := readShutdownLog(log); len(lines) != 0 {
		t.Fatalf("session_shutdown reached %q while every connection was open", lines)
	}
	// Closing a connection shuts down its two instances only, and the others keep serving.
	conns[0].closeInput()
	conns[0].waitForEOF()
	lines := requireShutdownLines(t, s, log, 2)
	if len(lines) != 2 {
		t.Fatalf("session_shutdown lines after one close = %q, want 2", lines)
	}
	requireCellCounts(t, s, 1, 1, "after the first connection closed")
	if got := callTool(conns[1], "node_counter"); got != "2" {
		t.Fatalf("connection 2 node_counter after the first closed = %q, want 2", got)
	}
	conns[1].closeInput()
	conns[1].waitForEOF()
	requireShutdownLines(t, s, log, 4)
	requireCellCounts(t, s, 1, 1, "after the second connection closed")
	conns[2].closeInput()
	conns[2].waitForEOF()
	lines = requireShutdownLines(t, s, log, 6)
	if len(lines) != 6 {
		t.Fatalf("session_shutdown lines = %q, want 6", lines)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		if seen[line] {
			t.Fatalf("instance %s shut down twice: %q", line, lines)
		}
		seen[line] = true
	}
	requireCellCounts(t, s, 0, 0, "after the last connection closed")
	s.signal(syscall.SIGTERM)
	s.waitForExit()
}

func TestListenRecoversASharedExtensionCrash(t *testing.T) {
	s, _ := listenExtensionServer(t, shareExtensions)
	first, second := s.dial(), s.dial()
	for _, conn := range []*rpcProcess{first, second} {
		if got := callTool(conn, "node_counter"); got != "1" {
			t.Fatalf("node_counter = %q, want 1\n%s", got, s.stderr())
		}
	}
	node, _ := cellProcesses(t, s.cmd.Process.Pid)
	if len(node) != 1 {
		t.Fatalf("%d Node cells before the crash\n%s", len(node), cellProcessReport(t, s.cmd.Process.Pid))
	}
	first.sendJSON(map[string]any{"id": "slow", "type": "prompt", "message": "Run: ext tool node_slow"})
	first.await("node_slow start", func(r rpcRecord) bool { return r["type"] == "tool_execution_start" && r["toolName"] == "node_slow" })
	victim, err := os.FindProcess(node[0].pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := victim.Kill(); err != nil {
		t.Fatal(err)
	}
	var slowErr bool
	first.await("node_slow end", func(r rpcRecord) bool {
		if r["type"] == "tool_execution_end" && r["toolName"] == "node_slow" {
			slowErr = r["isError"] == true
			return true
		}
		return false
	})
	if !slowErr {
		t.Fatal("the slow tool call did not fail when its process was killed")
	}
	first.await("agent_end after node_slow", func(r rpcRecord) bool { return r["type"] == "agent_end" })
	// Both sessions' instances restart fresh in the one recovered process.
	for i, conn := range []*rpcProcess{first, second} {
		// A call made while the supervisor waits out its restart delay fails; the first answer is the fresh instance's.
		deadline := time.Now().Add(s.budget)
		got := callTool(conn, "node_counter")
		for strings.HasPrefix(got, "error:") && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
			got = callTool(conn, "node_counter")
		}
		if got != "1" {
			t.Fatalf("session %d node_counter after the crash = %q, want 1\n%s", i+1, got, s.stderr())
		}
	}
	requireCellCounts(t, s, 1, 1, "after the crash")
}

func TestListenSessionShutdownReachesEverySessionOnSIGTERM(t *testing.T) {
	s, log := listenExtensionServer(t, shareExtensions)
	for range 2 {
		conn := s.dial()
		for _, tool := range []string{"node_counter", "go_counter"} {
			callTool(conn, tool)
		}
	}
	s.signal(syscall.SIGTERM)
	if code := s.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), s.stderr())
	}
	if lines := readShutdownLog(log); len(lines) != 4 {
		t.Fatalf("session_shutdown lines = %q, want one per instance (4)", lines)
	}
}

// closeDuringCall runs the tool in connection a until start reports it begun, closes a's input and returns once the server closed the connection and both of a's instances ran session_shutdown. b is a second session that keeps the shared processes.
func closeDuringCall(t *testing.T, s *listenServer, log string, a, b *rpcProcess, tool string, started func(rpcRecord) bool) {
	t.Helper()
	for _, conn := range []*rpcProcess{a, b} {
		if got := callTool(conn, "node_counter"); got != "1" {
			t.Fatalf("node_counter = %q, want 1\n%s", got, s.stderr())
		}
	}
	a.sendJSON(map[string]any{"id": "call", "type": "prompt", "message": "Run: ext tool " + tool})
	a.await(tool+" started", started)
	a.closeInput()
	a.waitForEOF()
	requireShutdownLines(t, s, log, 2)
}

// finishListenSession checks that nothing outlived the closed connection: the other session keeps working in the shared processes, they end with it, and the server still exits on a signal.
func finishListenSession(t *testing.T, s *listenServer, log string, b *rpcProcess) {
	t.Helper()
	requireCellCounts(t, s, 1, 1, "after the connection closed during the call")
	if got := callTool(b, "node_counter"); got != "2" {
		t.Fatalf("the other session's node_counter = %q, want 2 from the surviving process\n%s", got, s.stderr())
	}
	b.closeInput()
	b.waitForEOF()
	requireShutdownLines(t, s, log, 4)
	requireCellCounts(t, s, 0, 0, "after the last connection closed")
	s.signal(syscall.SIGTERM)
	if code := s.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), s.stderr())
	}
}

// Closing a connection during a running tool call is that session's input end: the session shuts down (the call is cancelled), the other session keeps its instances, and no process, Host or connection outlives the close.
func TestListenCloseDuringASlowToolCallEndsOnlyThatSession(t *testing.T) {
	s, log := listenExtensionServer(t, shareExtensions)
	a, b := s.dial(), s.dial()
	closeDuringCall(t, s, log, a, b, "node_slow", func(r rpcRecord) bool {
		return r["type"] == "tool_execution_start" && r["toolName"] == "node_slow"
	})
	finishListenSession(t, s, log, b)
}

// Closing a connection during a ctx.ui.confirm dialog: the dialog is pending when the input ends. The unwind resolves it as cancelled, but whether the extension's handler resumes before its generation ends is a race, as it is in Pi when its process exits first: it never resumes with true, nothing outlives the close, and the other session is unaffected (D232).
func TestListenCloseDuringAConfirmDialogLeavesNothingBehind(t *testing.T) {
	s, log := listenExtensionServer(t, shareExtensions)
	a, b := s.dial(), s.dial()
	closeDuringCall(t, s, log, a, b, "node_confirm", func(r rpcRecord) bool {
		return r["type"] == "extension_ui_request" && r["method"] == "confirm"
	})
	finishListenSession(t, s, log, b)
	for _, answer := range readShutdownLog(log + ".confirm") {
		if !strings.HasSuffix(answer, ":false") {
			t.Fatalf("the dialog of the closed connection was answered %q, want only cancelled (false)", answer)
		}
	}
}

// By default every session has its own extension processes, so module-level state is not shared between sessions (D232).
func TestListenIsolatesExtensionProcessesAndModuleStateByDefault(t *testing.T) {
	s, log := listenExtensionServer(t)
	conns := make([]*rpcProcess, 3)
	for i := range conns {
		conns[i] = s.dial()
		for _, tool := range []string{"node_counter", "go_counter", "node_module_counter", "go_module_counter"} {
			if got := callTool(conns[i], tool); got != "1" {
				t.Fatalf("connection %d %s = %q, want 1: module-level state must not leak between sessions\n%s", i+1, tool, got, s.stderr())
			}
		}
		requireCellCounts(t, s, i+1, i+1, fmt.Sprintf("after connection %d", i+1))
	}
	// Each session counts on in its own process.
	for _, tool := range []string{"node_module_counter", "go_module_counter"} {
		if got := callTool(conns[0], tool); got != "2" {
			t.Fatalf("connection 1 %s again = %q, want 2\n%s", tool, got, s.stderr())
		}
	}
	for i, conn := range conns[1:] {
		if got := callTool(conn, "node_module_counter"); got != "2" {
			t.Fatalf("connection %d node_module_counter again = %q, want 2: another session's calls leaked in\n%s", i+2, got, s.stderr())
		}
	}
	// Closing a connection ends its processes and no one else's.
	conns[0].closeInput()
	conns[0].waitForEOF()
	requireShutdownLines(t, s, log, 2)
	requireCellCounts(t, s, 2, 2, "after the first connection closed")
	if got := callTool(conns[1], "go_module_counter"); got != "2" {
		t.Fatalf("connection 2 go_module_counter = %q, want 2 from its own process\n%s", got, s.stderr())
	}
	conns[1].closeInput()
	conns[1].waitForEOF()
	conns[2].closeInput()
	conns[2].waitForEOF()
	requireShutdownLines(t, s, log, 6)
	requireCellCounts(t, s, 0, 0, "after the last connection closed")
	s.signal(syscall.SIGTERM)
	s.waitForExit()
}
