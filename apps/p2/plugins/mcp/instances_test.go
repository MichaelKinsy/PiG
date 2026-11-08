//go:build !windows

package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports test/p2/mcp-instances.test.ts: the MCP supervisor on its own, with the stdio fixture (testdata/mcp-server.mjs) and a
// short backoff: reconnects after a crash with the tools kept and the stderr only in the owner's error, tool-list changes, a
// close or restart while connecting, and the tools a reload's new supervisor starts with.

var (
	testBackoff = Backoff{First: 20 * time.Millisecond, Max: 200 * time.Millisecond, Stable: time.Minute}
	testClient  = ClientInfo{Name: "p2-test", Version: "1"}
)

// noEnvironments: these instances run on the p2 host only.
func noEnvironments(string, *chordjson.Object) *env.Connection {
	panic("no environments in this test")
}

// noOAuth: no HTTP servers either.
func noOAuth(OAuthServer) (oauth.McpAuthProvider, error) {
	return nil, errors.New("no OAuth servers in this test")
}

type supervised struct {
	instances *Instances
	placement Placement
	log       string
	id        string
}

func (s supervised) pids() []string { return pidsOf(s.log) }

// supervisor is a supervisor running the fixture once for session s1 at a temporary directory; env is added to its
// environment.
func supervisor(t *testing.T, env map[string]string, seed []Instance) supervised {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "starts.log")
	variables := map[string]string{"MCP_FIXTURE_LOG": log}
	for key, value := range env {
		variables[key] = value
	}
	config := mustOwner(t, mcpJSON(t, map[string]any{"files": stdioEntry(t, log, map[string]any{"env": variables})}))
	placement := Place(config, []Lease{{Session: "s1", Location: p2runtime.Location{Cwd: root}, Project: none, Generation: 1, Incarnation: "i"}},
		chordjson.NewObject(0), WorkEnvironments{"s1": {""}}, nil)
	instances := NewInstances(testClient, testBackoff, seed, noEnvironments, noOAuth)
	t.Cleanup(instances.Close)
	var id string
	for key := range placement.Instances {
		id = key
	}
	return supervised{instances: instances, placement: placement, log: log, id: id}
}

// when resolves with the instances once predicate holds for them.
func when(t *testing.T, instances *Instances, predicate func([]Instance) bool) []Instance {
	t.Helper()
	changed := make(chan struct{}, 1)
	stop := instances.Subscribe(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer stop()
	timeout := time.After(10 * time.Second)
	for {
		if list := instances.List(); predicate(list) {
			return list
		}
		select {
		case <-changed:
		case <-time.After(25 * time.Millisecond):
		case <-timeout:
			t.Fatalf("timed out; last: %+v", instances.List())
		}
	}
}

func pidOf(t *testing.T, text string) int {
	t.Helper()
	pid, err := strconv.Atoi(text)
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func textOf(result *pimcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	return result.Content[0].Text
}

func TestACrashedServerReconnectsAfterTheBackoffWithItsToolsKeptItsStderrIsTheOwnersOnly(t *testing.T) {
	s := supervisor(t, nil, nil)
	s.instances.Reconcile(s.placement)
	when(t, s.instances, func(list []Instance) bool { return len(list) > 0 && list[0].Status == "connected" })
	if _, err := s.instances.Call(context.Background(), s.id, "crash", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	down := when(t, s.instances, func(list []Instance) bool { return len(list) > 0 && list[0].Status != "connected" })[0]
	if len(down.Tools) != 4 {
		t.Fatalf("tools %d", len(down.Tools))
	}
	_, err := s.instances.Call(context.Background(), s.id, "echo", map[string]any{})
	if err == nil || err.Error() != `MCP server "files" is not connected: the connection was lost` {
		t.Fatalf("error %v", err)
	}
	if !regexp.MustCompile(`stderr: fixture crashing on purpose`).MatchString(down.Error) {
		t.Fatalf("owner's error %q", down.Error)
	}
	exited(t, pidOf(t, s.pids()[0]))
	back := when(t, s.instances, func(list []Instance) bool { return len(list) > 0 && list[0].Status == "connected" })[0]
	if len(back.Tools) != 4 || back.Error != "" {
		t.Fatalf("back: %+v", back)
	}
	if len(s.pids()) != 2 {
		t.Fatalf("starts %v", s.pids())
	}
	// A tool-list change refreshes the tools.
	if _, err := s.instances.Call(context.Background(), s.id, "grow", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	when(t, s.instances, func(list []Instance) bool {
		for _, tool := range list[0].Tools {
			if tool.Name == "extra" {
				return true
			}
		}
		return false
	})
}

func TestCloseAndRestartEndAConnectUnderWay(t *testing.T) {
	s := supervisor(t, map[string]string{"MCP_FIXTURE_HANG": "1"}, nil)
	s.instances.Reconcile(s.placement)
	waitFor(t, func() bool { return len(s.pids()) == 1 }, "the server to start")
	if status := s.instances.List()[0].Status; status != "starting" {
		t.Fatalf("status %s", status)
	}
	if err := s.instances.Restart(s.id); err != nil {
		t.Fatal(err)
	}
	exited(t, pidOf(t, s.pids()[0]))
	waitFor(t, func() bool { return len(s.pids()) == 2 }, "the server to start again")
	s.instances.Close()
	exited(t, pidOf(t, s.pids()[1]))
	if list := s.instances.List(); len(list) != 0 {
		t.Fatalf("list %+v", list)
	}
}

func TestANewSupervisorListsTheToolsItsPredecessorListedUntilTheServerAnswers(t *testing.T) {
	first := supervisor(t, map[string]string{"MCP_FIXTURE_HANG": "1"}, nil)
	seed := Instance{
		ID: first.id, Server: "files", Source: "owner", RunsOn: RunOn{Kind: RunOnHost}, Status: "connected",
		Tools: []pimcp.Tool{{Name: "echo", InputSchema: []byte(`{"type":"object"}`)}},
	}
	instances := NewInstances(testClient, testBackoff, []Instance{seed}, noEnvironments, noOAuth)
	t.Cleanup(instances.Close)
	instances.Reconcile(first.placement)
	list := instances.List()
	if len(list) != 1 || list[0].Status != "starting" || len(list[0].Tools) != 1 || list[0].Tools[0].Name != "echo" {
		t.Fatalf("list %+v", list)
	}
}

// waitFor polls until check holds.
func waitFor(t *testing.T, check func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
