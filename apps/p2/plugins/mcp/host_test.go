//go:build !windows

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	envcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// The server host of test/p2/mcp.test.ts: the MCP plugin and mcp.json in a fresh data directory, with stubs of what the
// manager reads of the other plugins (envs.json, the catalog's and the subagents' locations, sessions' locks, the server's
// data directory and the workers' states). Sessions' workers are views of the Mcp contract as principals of workers.

// lingerTest is how long views linger after their last reference here (30 s in p2).
const lingerTest = 150 * time.Millisecond

var (
	ownerPrincipal = p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}
	guestPrincipal = p2runtime.GuestPrincipal{ID: "g1", Name: "Guest", Home: "s1", Sessions: map[string]p2runtime.GuestMode{"s1": "full"}, Revision: 1}
)

// noEnvironmentsConnector is the connector of environments in tests that configure none.
func noEnvironmentsConnector(host.Self) ConnectEnv {
	return func(string, *chordjson.Object) *env.Connection {
		return env.NewConnection(env.ConnectionOptions{CommandFunc: func(context.Context) ([]string, error) {
			return nil, os.ErrNotExist
		}})
	}
}

func inertMethods(names ...string) map[string]p2test.StubMethod {
	methods := map[string]p2test.StubMethod{}
	for _, name := range names {
		methods[name] = func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
	}
	return methods
}

// sessionRow is a catalog entry of session id working on environment (nil: this machine).
func sessionRow(id string, environment any) *delta.JsonObject {
	return delta.JsonObjectOf("id", id, "env", environment, "cwd", "/", "title", id, "createdAt", float64(0), "updatedAt", float64(0),
		"archivedAt", nil, "pinned", false, "model", delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"),
		"thinkingLevel", "off", "main", float64(1), "branches", []any{})
}

func locJSON(environment any, cwd string) *delta.JsonObject {
	return delta.JsonObjectOf("env", environment, "cwd", cwd)
}

// mcpOptions are what mcpHost takes besides the servers.
type mcpOptions struct {
	// envs are envs.json's environments, reached through connector.
	envs      *delta.JsonObject
	connector EnvConnector
	// leaksChecked: the test already called p2test.NoLeakedGoroutines, before it started servers of its own.
	leaksChecked bool
}

// stubCells are the cells the stubs make when the manager first takes them.
type stubCells struct {
	mu       sync.Mutex
	sessions *p2test.Cell
	children *p2test.Cell
	locks    *p2test.Cell
	workers  *p2test.Cell
}

func (s *stubCells) get(pick func(*stubCells) *p2test.Cell) *p2test.Cell {
	var cell *p2test.Cell
	if err := p2test.Poll(func() (bool, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		cell = pick(s)
		return cell != nil, nil
	}, 0, "the stubs the manager reads"); err != nil {
		panic(err)
	}
	return cell
}

// mview is a held Mcp reference: a worker's or the owner's.
type mview struct {
	*host.Reference
	t testing.TB
}

// member is the current value of a state member of the mview.
func (v mview) member(name string) delta.JsonValue {
	v.t.Helper()
	value, err := v.Member(name).Value()
	if err != nil {
		v.t.Fatal(err)
	}
	return value
}

// until waits for a state member's value to satisfy predicate.
func (v mview) until(name string, predicate func(delta.JsonValue) bool) delta.JsonValue {
	v.t.Helper()
	value, err := p2test.Until(v.Member(name), predicate, 0)
	if err != nil {
		v.t.Fatalf("%s: %v", name, err)
	}
	return value
}

func (v mview) setLocations(locations ...*delta.JsonObject) error {
	_, err := v.Call("setLocations", delta.JsonObjectOf("locations", locationsOf(locations)), p2test.Background)
	return err
}

func locationsOf(locations []*delta.JsonObject) []any {
	list := make([]any, 0, len(locations))
	for _, location := range locations {
		list = append(list, location)
	}
	return list
}

// callTool is the text of a call of tool, or its error.
func (v mview) callTool(location *delta.JsonObject, server, tool string, args *delta.JsonObject) (string, error) {
	if args == nil {
		args = delta.NewJsonObject(0)
	}
	result, err := v.Call("call", delta.JsonObjectOf("location", location, "server", server, "tool", tool, "args", args), p2test.Background)
	if err != nil {
		return "", err
	}
	return resultText(result), nil
}

// resultText is the first content text of a tool result.
func resultText(result delta.JsonValue) string {
	content, _ := objectOf(result).Value("content").([]any)
	if len(content) == 0 {
		return ""
	}
	return stringOf(objectOf(content[0]).Value("text"))
}

// whoamiReport is the fixture's own report: its pid, its parent's, cwd, the client's roots and its MCP_FIXTURE_* variables.
type whoamiReport struct {
	Pid   int    `json:"pid"`
	Ppid  int    `json:"ppid"`
	Cwd   string `json:"cwd"`
	Roots []struct {
		URI string `json:"uri"`
	} `json:"roots"`
	Vars map[string]string `json:"vars"`
	// NoRoots is whether the client offered none (null).
	NoRoots bool `json:"-"`
}

type mcpHost struct {
	t        testing.TB
	f        *p2test.TestHost
	root     string
	dataDir  string
	log      string
	cells    *stubCells
	manager  *host.Reference
	mu       sync.Mutex
	workers  []*host.Reference
	closed   bool
	linger   time.Duration
	facetEnv EnvConnector
}

// newMcpHost is the host with mcp.json holding servers(log).
func newMcpHost(t *testing.T, servers func(log string) map[string]any, options ...mcpOptions) *mcpHost {
	t.Helper()
	var option mcpOptions
	if len(options) > 0 {
		option = options[0]
	}
	if !option.leaksChecked {
		p2test.NoLeakedGoroutines(t)
	}
	if option.envs == nil {
		option.envs = delta.NewJsonObject(0)
	}
	if option.connector == nil {
		option.connector = noEnvironmentsConnector
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &mcpHost{t: t, root: root, dataDir: filepath.Join(root, "data"), log: filepath.Join(root, "starts.log"), cells: &stubCells{}, linger: lingerTest, facetEnv: option.connector}
	if err := os.Mkdir(h.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	h.configure(servers)
	cells := h.cells
	stubs := []p2test.Stub{
		// What the manager reads of these: envs.json, sessions' and subagents' locations; their methods are unused.
		{Contract: envcontract.EnvService, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{State: map[string]any{"configs": option.envs, "tests": delta.NewJsonObject(0), "failure": nil}, Methods: inertMethods("test", "reload")}
		}},
		{Contract: sessionscontract.Catalog, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			cell := self.Cell([]any{})
			cells.mu.Lock()
			cells.sessions = cell
			cells.mu.Unlock()
			return p2test.StubMembers{State: map[string]any{"sessions": cell}, Methods: inertMethods("get", "create", "activity", "update")}
		}},
		{Contract: subagentscontract.ChildStore, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			cell := self.Cell(delta.NewJsonObject(0))
			cells.mu.Lock()
			cells.children = cell
			cells.mu.Unlock()
			return p2test.StubMembers{State: map[string]any{"children": cell}, Methods: inertMethods("report")}
		}},
		// Sessions' locks (a Discord link's picks), changed by lock below.
		{Contract: sessionscontract.SessionLocks, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			cell := self.Cell(delta.NewJsonObject(0))
			cells.mu.Lock()
			cells.locks = cell
			cells.mu.Unlock()
			return p2test.StubMembers{State: map[string]any{"locks": cell}, Methods: inertMethods("set")}
		}},
		{Contract: p2runtime.ServerLocal, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{State: map[string]any{"info": delta.JsonObjectOf("app", "/app", "dataDir", h.dataDir, "fake", true, "allowedHosts", []any{}, "port", float64(1))}, Methods: inertMethods("revokeOwners", "revokeGuests")}
		}},
		{Contract: p2runtime.Hosts, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			cell := self.Cell(delta.NewJsonObject(0))
			cells.mu.Lock()
			cells.workers = cell
			cells.mu.Unlock()
			return p2test.StubMembers{State: map[string]any{"workers": cell}, Methods: inertMethods("stop")}
		}},
	}
	h.f = p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{ServerFacet(lingerTest, option.connector)}, Stubs: stubs})
	manager, err := h.f.Host.Get(p2test.Background, contract.McpManager, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p2runtime.WhenLive(p2test.Background, manager); err != nil {
		t.Fatal(err)
	}
	h.manager = manager
	t.Cleanup(func() {
		h.mu.Lock()
		closed := h.closed
		workers := h.workers
		h.mu.Unlock()
		if !closed {
			// Workers go first, as when their processes exit: their views release the leases while the manager lives.
			for _, ref := range workers {
				ref.Release()
			}
			if _, err := p2test.Until(manager.Member("leases"), func(value delta.JsonValue) bool { return len(value.([]any)) == 0 }, 0); err != nil {
				t.Errorf("leases after the workers' end: %v", err)
			}
			manager.Release()
		}
	})
	return h
}

// shutdown closes the host as a server's end does.
func (h *mcpHost) shutdown() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.manager.Release()
	h.f.Close()
}

// configure writes mcp.json as `{ mcpServers: servers(log) }`.
func (h *mcpHost) configure(servers func(log string) map[string]any) {
	h.write(mcpJSON(h.t, servers(h.log)))
}

// write writes text as mcp.json as it is (a save caught half-way).
func (h *mcpHost) write(text string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.dataDir, "mcp.json"), []byte(text), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// stdio is the fixture server as a stdio entry; every start appends its pid to the host's log.
func (h *mcpHost) stdio(extra ...map[string]any) map[string]any {
	h.t.Helper()
	merged := map[string]any{}
	for _, more := range extra {
		for key, value := range more {
			merged[key] = value
		}
	}
	return stdioEntry(h.t, h.log, merged)
}

// at is a directory on this machine as a location.
func (h *mcpHost) at(name string) *delta.JsonObject {
	cwd := filepath.Join(h.root, name)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		h.t.Fatal(err)
	}
	return locJSON(nil, cwd)
}

// starts is the fixture process starts so far.
func (h *mcpHost) starts() []string { return pidsOf(h.log) }

func (h *mcpHost) change(cell *p2test.Cell, fn func(current any) any) {
	h.t.Helper()
	if err := cell.Change(fn); err != nil {
		h.t.Fatal(err)
	}
}

// listed records id in the catalog as working on environment, unless it is there already.
func (h *mcpHost) listed(id string, environment any) {
	sessions := h.cells.get(func(s *stubCells) *p2test.Cell { return s.sessions })
	h.change(sessions, func(current any) any {
		rows, _ := current.([]any)
		for _, row := range rows {
			if objectOf(row).Value("id") == id {
				return current
			}
		}
		return append(slices.Clone(rows), sessionRow(id, environment))
	})
}

// running publishes session's worker generation as running, the session in the catalog (on this machine) if new.
func (h *mcpHost) running(session string, generation int) {
	h.listed(session, nil)
	h.publish(session, generation)
}

func (h *mcpHost) publish(session string, generation int) {
	workers := h.cells.get(func(s *stubCells) *p2test.Cell { return s.workers })
	h.change(workers, func(current any) any {
		next := objectOf(current).Clone()
		next.Set(session, delta.JsonObjectOf("running", true, "generation", float64(generation)))
		return next
	})
}

// move moves session id to environment in the catalog.
func (h *mcpHost) move(id string, environment any) {
	sessions := h.cells.get(func(s *stubCells) *p2test.Cell { return s.sessions })
	h.change(sessions, func(current any) any {
		rows, _ := current.([]any)
		next := make([]any, 0, len(rows))
		for _, row := range rows {
			if objectOf(row).Value("id") == id {
				row = objectOf(row).Clone()
				objectOf(row).Set("env", environment)
			}
			next = append(next, row)
		}
		return next
	})
}

// lock locks session id to lock (nil: unlocks it), as linking it to Discord or editing its link does.
func (h *mcpHost) lock(id string, lock *delta.JsonObject) {
	locks := h.cells.get(func(s *stubCells) *p2test.Cell { return s.locks })
	h.change(locks, func(current any) any {
		next := objectOf(current).Clone()
		if lock == nil {
			next.Delete(id)
		} else {
			next.Set(id, lock)
		}
		return next
	})
}

// lockOf is a session lock.
func lockOf(servers ...string) *delta.JsonObject {
	list := make([]any, 0, len(servers))
	for _, server := range servers {
		list = append(list, server)
	}
	return delta.JsonObjectOf("mcp", list, "projectMcp", false, "instructions", false, "capabilities", []any{})
}

// subagents records id's subagents (their own locations).
func (h *mcpHost) subagents(id string, list []any) {
	children := h.cells.get(func(s *stubCells) *p2test.Cell { return s.children })
	h.change(children, func(current any) any {
		next := objectOf(current).Clone()
		next.Set(id, list)
		return next
	})
}

// owner is the owner's mview.
func (h *mcpHost) owner() mview {
	h.t.Helper()
	ref, err := p2test.HoldIn(h.f.Client(ownerPrincipal), contract.Mcp, delta.NewJsonObject(0), "owner")
	if err != nil {
		h.t.Fatal(err)
	}
	h.mu.Lock()
	h.workers = append(h.workers, ref)
	h.mu.Unlock()
	return mview{ref, h.t}
}

// workerOptions are what worker takes.
type workerOptions struct {
	// stale: the worker is not published as the session's running one.
	stale bool
	// env is where the session works, unless the catalog lists it already (nil: this machine).
	env any
}

// worker is the mview of session's worker generation (published as the session's running worker unless stale).
func (h *mcpHost) worker(session string, generation int, options ...workerOptions) mview {
	h.t.Helper()
	var option workerOptions
	if len(options) > 0 {
		option = options[0]
	}
	h.listed(session, option.env)
	if !option.stale {
		h.publish(session, generation)
	}
	ref, err := p2test.HoldIn(h.f.Client(p2runtime.WorkerPrincipal{Session: session, Generation: generation}), contract.Mcp, delta.NewJsonObject(0), "worker")
	if err != nil {
		h.t.Fatal(err)
	}
	h.mu.Lock()
	h.workers = append(h.workers, ref)
	h.mu.Unlock()
	return mview{ref, h.t}
}

// instancesWhere is a predicate over a list of instances.
type instancesWhere = func(delta.JsonValue) bool

func instanceList(value delta.JsonValue) []*delta.JsonObject {
	list, _ := value.([]any)
	instances := make([]*delta.JsonObject, 0, len(list))
	for _, each := range list {
		instances = append(instances, objectOf(each))
	}
	return instances
}

// statusOf is the status of every instance of server.
func statusOf(value delta.JsonValue, server string) []string {
	var statuses []string
	for _, instance := range instanceList(value) {
		if instance.Value("server") == server {
			statuses = append(statuses, stringOf(instance.Value("status")))
		}
	}
	return statuses
}

// isUp is whether exactly count instances of server are connected.
func isUp(server string, count ...int) instancesWhere {
	want := 1
	if len(count) > 0 {
		want = count[0]
	}
	return func(value delta.JsonValue) bool {
		connected := 0
		for _, status := range statusOf(value, server) {
			if status == "connected" {
				connected++
			}
		}
		return connected == want
	}
}

func both(predicates ...instancesWhere) instancesWhere {
	return func(value delta.JsonValue) bool {
		for _, predicate := range predicates {
			if !predicate(value) {
				return false
			}
		}
		return true
	}
}

// instanceOf is the first instance of server.
func instanceOf(value delta.JsonValue, server string) *delta.JsonObject {
	for _, instance := range instanceList(value) {
		if instance.Value("server") == server {
			return instance
		}
	}
	return nil
}

// whoami is the fixture's report of itself at location.
func whoami(t testing.TB, v mview, location *delta.JsonObject, server string) whoamiReport {
	t.Helper()
	text, err := v.callTool(location, server, "whoami", nil)
	if err != nil {
		t.Fatal(err)
	}
	var report whoamiReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("whoami %q: %v", text, err)
	}
	report.NoRoots = strings.Contains(strings.ReplaceAll(text, " ", ""), `"roots":null`)
	return report
}

// cwdOf is a location's cwd.
func cwdOf(location *delta.JsonObject) string { return stringOf(location.Value("cwd")) }

// errorsText is the lines of an errors state member.
func errorsText(value delta.JsonValue) []string {
	list, _ := value.([]any)
	lines := make([]string, 0, len(list))
	for _, each := range list {
		lines = append(lines, stringOf(each))
	}
	return lines
}

func p2runtimeWorker(session string, generation int) p2runtime.WorkerPrincipal {
	return p2runtime.WorkerPrincipal{Session: session, Generation: generation}
}
