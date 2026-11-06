//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func requireNoServerRace(t *testing.T, server *listenServer) {
	t.Helper()
	if strings.Contains(server.stderr(), "DATA RACE") {
		t.Fatalf("the server reported a data race:\n%s", server.stderr())
	}
}

// Sessions of the default (isolated) --listen server build concurrently: a build only spawns the session's own extension processes, so one session's start does not wait for another's (D232). The fixture's factory in the second and third connection's builds waits until both have started, so builds that run one at a time never finish. With --listen-share-extensions the builds are serialized (the registry's lookups and registrations), so the same pair would wait for each other forever and is not exercised.
func TestListenDefaultBuildsSessionsConcurrently(t *testing.T) {
	dir := listenTempDir(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "listen-barrier.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(dir, "barrier.log")
	path := filepath.Join(dir, "pig.sock")
	server := startListenServerBinary(t, listenTestBinary(t), append(listenEnv(t, dir), "LISTEN_BARRIER_COUNT="+filepath.Join(dir, "count"), "LISTEN_BARRIER_RESULT="+result),
		"--mode", "rpc", "--listen", path, "--model", "test-faux/echo", "--no-session", "-e", fixture)
	server.path = path
	requestResponse(server.dial(), "ready", "get_state")
	second, third := server.dial(), server.dial()
	for _, p := range []*rpcProcess{second, third} {
		p.sendJSON(map[string]any{"id": "ready", "type": "get_state"})
	}
	for _, p := range []*rpcProcess{second, third} {
		p.await("get_state response", func(r rpcRecord) bool { return r["type"] == "response" && r["id"] == "ready" })
	}
	if data, _ := os.ReadFile(result); strings.Count(string(data), "passed") != 2 {
		t.Fatalf("barrier result %q, want both sessions through it\n%s", data, server.stderr())
	}
	requireNoServerRace(t, server)
}

// goLinks counts the Go links a default --listen server runs while sessions start: the startup build sees no extension, then the Go counter fixture appears and `sessions` connections start at the same time, each building it. One link is one compiled cell binary; two sessions that need the same extension must not compile it twice (D232: the content-addressed publisher takes a per-digest lock).
func goLinks(t *testing.T, sessions int) int {
	t.Helper()
	dir := listenTempDir(t)
	log := filepath.Join(dir, "links.log")
	script := filepath.Join(dir, "toolexec.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in */link) echo link >> \"$LINK_LOG\" ;; esac\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pig.sock")
	env := append(listenEnv(t, dir), "GOFLAGS=-toolexec="+script, "LINK_LOG="+log, "PIG_TEST_FAUX_SCENARIO=parity-basic")
	server := startListenServerBinary(t, listenTestBinary(t), env, "--mode", "rpc", "--listen", path, "--model", "test-faux/faux-1", "--no-session")
	server.path = path
	requestResponse(server.dial(), "ready", "get_state")
	installListenGoExtension(t, filepath.Join(dir, "agent", "extensions"))
	conns := make([]*rpcProcess, sessions)
	for i := range conns {
		conns[i] = server.dial()
		conns[i].sendJSON(map[string]any{"id": "ready", "type": "get_state"})
	}
	for _, p := range conns {
		p.await("get_state response", func(r rpcRecord) bool { return r["type"] == "response" && r["id"] == "ready" })
	}
	for i, p := range conns {
		if got := callTool(p, "go_counter"); got != "1" {
			t.Fatalf("session %d go_counter = %q, want 1: the extension did not load\n%s", i+1, got, server.stderr())
		}
	}
	requireNoServerRace(t, server)
	data, _ := os.ReadFile(log)
	return strings.Count(string(data), "link")
}

func TestListenConcurrentSessionsBuildAnExtensionOnce(t *testing.T) {
	one := goLinks(t, 1)
	if one == 0 {
		t.Fatal("the toolexec wrapper saw no link: the build did not run through it")
	}
	if two := goLinks(t, 2); two != one {
		t.Fatalf("two concurrent sessions ran %d Go links, one session ran %d: the extension was built more than once", two, one)
	}
}

// Many sessions start, run a tool and replace their session at the same time in the default server, which builds them concurrently: the server process reports no data race (D232). Under -race the server is built with the race detector (concurrentListenBinary); without it the test still proves the sessions all work.
func TestListenConcurrentSessionBuildsReportNoDataRace(t *testing.T) {
	const sessions = 10
	server, _ := listenExtensionServerBinary(t, listenTestBinary(t))
	conns := make([]*rpcProcess, sessions)
	for i := range conns {
		conns[i] = server.dial()
		conns[i].sendJSON(map[string]any{"id": "ready", "type": "get_state"})
	}
	var wg sync.WaitGroup
	for i, p := range conns {
		wg.Go(func() {
			p.await("get_state response", func(r rpcRecord) bool { return r["type"] == "response" && r["id"] == "ready" })
			if got := callTool(p, "node_counter"); got != "1" {
				t.Errorf("session %d node_counter = %q, want 1", i+1, got)
			}
			// A replacement session builds again, while the other sessions are building or running.
			requestResponse(p, "new", "new_session")
			if got := callTool(p, "go_counter"); got != "1" {
				t.Errorf("session %d go_counter after new_session = %q, want 1", i+1, got)
			}
		})
	}
	wg.Wait()
	requireNoServerRace(t, server)
}
