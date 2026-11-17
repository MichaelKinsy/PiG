//go:build unix

package cli

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"
)

const trustProbeExtension = `export default function (pi) {
  pi.registerCommand("trust-probe", { description: "present only when the project's extensions load", handler: async () => {} });
}
`

// listenCommandNames is the names get_commands lists in a session.
func listenCommandNames(p *rpcProcess, id string) []string {
	p.t.Helper()
	response := requestResponse(p, id, "get_commands")
	data, _ := response["data"].(map[string]any)
	list, _ := data["commands"].([]any)
	var names []string
	for _, item := range list {
		if command, ok := item.(map[string]any); ok {
			if name, ok := command["name"].(string); ok {
				names = append(names, name)
			}
		}
	}
	return names
}

// A connection has no project-trust prompt of its own: its build takes the decision the server's startup build resolved for the working directory, so a client cannot widen
// it (D232). The server runs in a project whose .pig/extensions holds an extension. Started with --approve, every connection (the first, served by the startup build, and a
// later one, built by the connection factory) lists the extension's command; started with --no-approve, none does.
func TestListenConnectionTakesTheServersProjectTrustDecision(t *testing.T) {
	for _, tc := range []struct {
		flag   string
		loaded bool
	}{{"--approve", true}, {"--no-approve", false}} {
		t.Run(tc.flag, func(t *testing.T) {
			dir := listenTempDir(t)
			project := filepath.Join(dir, "project")
			if err := os.MkdirAll(filepath.Join(project, ".pig", "extensions", "trust-probe"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, ".pig", "extensions", "trust-probe", "index.js"), []byte(trustProbeExtension), 0o644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "pig.sock")
			server := startListenServerBinaryAt(t, project, listenTestBinary(t), listenEnv(t, dir), "--mode", "rpc", "--listen", path, "--model", "test-faux/faux-1", "--no-session", tc.flag)
			server.path = path
			for i, name := range []string{"first", "second"} {
				got := slices.Contains(listenCommandNames(server.dial(), name), "trust-probe")
				if got != tc.loaded {
					t.Fatalf("connection %d: the project extension's command listed = %v, want %v under %s\n%s", i+1, got, tc.loaded, tc.flag, server.stderr())
				}
			}
		})
	}
}

// --listen never opens a network socket: its value is a path. A value that looks like host:port names a Unix socket file of that name in the working directory, and no
// TCP listener appears on the port. A URL names a path inside a directory that does not exist, so the server refuses to start (D232).
func TestListenNeverOpensATCPSocket(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	address := "127.0.0.1:" + strconv.Itoa(port)
	requireRefused := func(t *testing.T, server *listenServer) {
		t.Helper()
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			_ = conn.Close()
			t.Fatalf("a TCP connection to %s was accepted: --listen opened a network socket\n%s", address, server.stderr())
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			t.Fatalf("dialing %s: %v, want connection refused", address, err)
		}
	}
	t.Run("host:port", func(t *testing.T) {
		dir := listenTempDir(t)
		server := startListenServerBinaryAt(t, dir, listenTestBinary(t), listenEnv(t, dir), "--mode", "rpc", "--listen", address, "--model", "test-faux/faux-1", "--no-session")
		server.path = filepath.Join(dir, address)
		requestResponse(server.dial(), "ready", "get_state")
		info, err := os.Lstat(server.path)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			t.Fatalf("the value %q did not become a Unix socket file in the working directory: %v, %v", address, info, err)
		}
		requireRefused(t, server)
	})
	t.Run("URL", func(t *testing.T) {
		dir := listenTempDir(t)
		server := startListenServerBinaryAt(t, dir, listenTestBinary(t), listenEnv(t, dir), "--mode", "rpc", "--listen", "tcp://"+address, "--model", "test-faux/faux-1", "--no-session")
		select {
		case <-server.exited:
		case <-time.After(server.budget):
			t.Fatalf("a --listen URL did not end the server\n%s", server.stderr())
		}
		if server.code != 1 {
			t.Fatalf("the server exited %d for a URL, want 1\n%s", server.code, server.stderr())
		}
		requireRefused(t, server)
	})
}

// A session replacement in a --listen-share-extensions server keeps the shared extension processes and answers: the response succeeds, no cell process is added or ended,
// and the replacement session's extensions are the same processes (their module-level counters go on counting) (D232).
func TestListenNewSessionKeepsTheSharedProcessesAndAnswers(t *testing.T) {
	s, _ := listenExtensionServer(t, shareExtensions)
	first, second := s.dial(), s.dial()
	for i, p := range []*rpcProcess{first, second} {
		for _, tool := range []string{"node_module_counter", "go_module_counter"} {
			if got := callTool(p, tool); got != strconv.Itoa(i+1) {
				t.Fatalf("connection %d %s = %q, want %d\n%s", i+1, tool, got, i+1, s.stderr())
			}
		}
	}
	requireCellCounts(t, s, 1, 1, "before the replacement")
	response := requestResponse(first, "new", "new_session")
	if response["success"] != true {
		t.Fatalf("new_session answered %v, want success\n%s", response, s.stderr())
	}
	requireCellCounts(t, s, 1, 1, "after the replacement")
	for _, tool := range []string{"node_module_counter", "go_module_counter"} {
		if got := callTool(first, tool); got != "3" {
			t.Fatalf("after new_session %s = %q, want 3: the replacement session runs in the shared process\n%s", tool, got, s.stderr())
		}
	}
}
