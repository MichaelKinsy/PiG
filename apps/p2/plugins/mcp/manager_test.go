//go:build !windows

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/contract"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	nexthost "github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports the remaining cases of test/p2/mcp.test.ts: the manager and its views on a server host (stubs of the plugins it
// reads) with the stdio fixture server.

func fileURL(path string) string { return (&url.URL{Scheme: "file", Path: path}).String() }

func expectMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("%q does not match %s", text, pattern)
	}
}

func expectError(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want one matching %s", pattern)
	}
	if pattern != "" {
		expectMatch(t, err.Error(), pattern)
	}
}

func expectEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func expectJSON(t *testing.T, got, want any) {
	t.Helper()
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatalf("got %s, want %s", a, b)
	}
}

func noError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func text(t *testing.T, v mview, location *delta.JsonObject, server, tool string, args *delta.JsonObject) string {
	t.Helper()
	got, err := v.callTool(location, server, tool, args)
	noError(t, err)
	return got
}

func echoArgs(text string) *delta.JsonObject { return delta.JsonObjectOf("text", text) }

func serverNames(value delta.JsonValue) []string {
	names := []string{}
	for _, instance := range instanceList(value) {
		names = append(names, stringOf(instance.Value("server")))
	}
	return names
}

func instanceTools(instance *delta.JsonObject) []string {
	names := []string{}
	list, _ := instance.Value("tools").([]any)
	for _, tool := range list {
		names = append(names, stringOf(objectOf(tool).Value("name")))
	}
	return names
}

func locationCwd(instance *delta.JsonObject) string {
	if location := objectOf(instance.Value("location")); location != nil {
		return cwdOf(location)
	}
	return ""
}

func locationEnv(instance *delta.JsonObject) string {
	if location := objectOf(instance.Value("location")); location != nil {
		return stringOf(location.Value("env"))
	}
	return ""
}

func lenIs(n int) func(delta.JsonValue) bool {
	return func(value delta.JsonValue) bool { list, _ := value.([]any); return len(list) == n }
}

func TestOwnerConfigValidServersLoadInvalidEntriesAreReportedAndSkipped(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any {
		return map[string]any{
			"good":       stdioEntry(t, log, nil),
			"bad name!":  map[string]any{"command": "x"},
			"nothing":    map[string]any{},
			"cimd":       map[string]any{"url": "https://example.com/mcp", "oauth": map[string]any{"clientRegistration": "cimd"}},
			"runOnHttp":  map[string]any{"url": "https://example.com/mcp", "runOn": "host"},
			"sharedHere": map[string]any{"command": "x", "shared": true, "runOn": "location"},
			"off":        map[string]any{"command": "x", "enabled": false},
		}
	})
	owner := h.owner()
	errs := strings.Join(errorsText(owner.until("errors", func(v delta.JsonValue) bool { return len(errorsText(v)) > 0 })), "\n")
	if n := len(errorsText(owner.member("errors"))); n != 5 {
		t.Fatalf("%d errors: %s", n, errs)
	}
	for _, pattern := range []string{`invalid server name`, `needs either "command"`, `cimd`, `runOn is for stdio servers`, `shared server cannot run where each conversation works`} {
		expectMatch(t, errs, pattern)
	}
	// Nothing runs before a session leases it; then only the valid, enabled server.
	expectEqual(t, len(owner.member("instances").([]any)), 0)
	s1 := h.worker("s1", 1)
	noError(t, s1.setLocations(h.at("one")))
	expectJSON(t, serverNames(s1.until("servers", isUp("good"))), []string{"good"})
}

func filesAndBrowser(t *testing.T) func(string) map[string]any {
	return func(log string) map[string]any {
		return map[string]any{"files": stdioEntry(t, log, nil), "browser": stdioEntry(t, log, map[string]any{"shared": true})}
	}
}

func TestUnsharedServersRunPerSessionAndLocationSharedOnesOnceRootsAndCalls(t *testing.T) {
	h := newMcpHost(t, filesAndBrowser(t))
	one, two := h.at("one"), h.at("two")
	s1, s2 := h.worker("s1", 1), h.worker("s2", 1)
	noError(t, s1.setLocations(one))
	noError(t, s2.setLocations(two))
	s1.until("servers", both(isUp("files"), isUp("browser")))
	owner := h.owner()
	all := owner.until("instances", both(isUp("files", 2), isUp("browser")))
	expectEqual(t, len(all.([]any)), 3)
	expectEqual(t, len(h.starts()), 3)
	var files *delta.JsonObject
	for _, instance := range instanceList(all) {
		if instance.Value("server") == "files" && locationCwd(instance) == cwdOf(one) {
			files = instance
		}
	}
	expectJSON(t, files.Value("runsOn"), "host")
	expectJSON(t, instanceTools(files), []string{"echo", "whoami", "crash", "grow"})
	if browser := instanceOf(all, "browser"); browser.Value("location") != nil {
		t.Fatalf("the shared server has a location: %v", browser.Value("location"))
	}
	// Each session's own process, at its location with its cwd as the root; the shared one has no roots.
	first := whoami(t, s1, one, "files")
	if first.Pid == whoami(t, s2, two, "files").Pid {
		t.Fatal("two sessions share a process")
	}
	expectEqual(t, first.Cwd, cwdOf(one))
	expectJSON(t, first.Roots, []map[string]string{{"uri": fileURL(cwdOf(one))}})
	expectEqual(t, whoami(t, s1, one, "browser").Pid, whoami(t, s2, two, "browser").Pid)
	if !whoami(t, s1, one, "browser").NoRoots {
		t.Fatal("the shared server was offered roots")
	}
	expectEqual(t, text(t, s1, one, "files", "echo", echoArgs("hi")), "echo: hi")
	// Another session's location is not leased by this worker.
	_, err := s1.callTool(two, "files", "echo", nil)
	expectError(t, err, "No MCP servers")
}

func TestASessionsLockChangesItsLeasedServersLiveOnlyThePickedOnesAllAgainWhenUnlocked(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any {
		return map[string]any{"files": stdioEntry(t, log, nil), "notes": stdioEntry(t, log, nil)}
	})
	here := h.at("here")
	s1 := h.worker("s1", 1)
	noError(t, s1.setLocations(here))
	s1.until("servers", both(isUp("files"), isUp("notes")))
	h.lock("s1", lockOf("files"))
	s1.until("servers", both(isUp("files"), func(v delta.JsonValue) bool { return instanceOf(v, "notes") == nil }))
	_, err := s1.callTool(here, "notes", "echo", nil)
	expectError(t, err, "")
	expectEqual(t, text(t, s1, here, "files", "echo", echoArgs("picked")), "echo: picked")
	// Picking another server (the link edited in place), then unlocking.
	h.lock("s1", lockOf("notes"))
	s1.until("servers", both(isUp("notes"), func(v delta.JsonValue) bool { return instanceOf(v, "files") == nil }))
	h.lock("s1", nil)
	s1.until("servers", both(isUp("files"), isUp("notes")))
}

func TestProcessesEndWithTheirInstancesALocationLeftOutTheViewsEndTheHostsShutdown(t *testing.T) {
	h := newMcpHost(t, filesAndBrowser(t))
	one, two := h.at("one"), h.at("two")
	owner := h.owner()
	s1 := h.worker("s1", 1)
	noError(t, s1.setLocations(one, two))
	owner.until("instances", both(isUp("files", 2), isUp("browser")))
	atOne := whoami(t, s1, one, "files").Pid
	atTwo := whoami(t, s1, two, "files").Pid
	shared := whoami(t, s1, one, "browser").Pid
	noError(t, s1.setLocations(one))
	owner.until("instances", func(v delta.JsonValue) bool { return len(statusOf(v, "files")) == 1 })
	exited(t, atTwo)
	// The worker's reference goes (its link closed): the view ends and releases its leases.
	s1.Release()
	left := owner.until("instances", func(v delta.JsonValue) bool { return len(statusOf(v, "files")) == 0 })
	expectJSON(t, statusOf(left, "browser"), []string{"connected"})
	exited(t, atOne)
	if !alive(shared) {
		t.Fatal("the shared process ended with a lease")
	}
	h.shutdown()
	exited(t, shared)
}

func TestEditingMcpJSONRestartsAChangedServerAndClosesARemovedOne(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any {
		return map[string]any{"files": stdioEntry(t, log, nil), "docs": stdioEntry(t, log, nil)}
	})
	one := h.at("one")
	s1 := h.worker("s1", 1)
	noError(t, s1.setLocations(one))
	before := s1.until("servers", both(isUp("files"), isUp("docs")))
	files := whoami(t, s1, one, "files").Pid
	docs := whoami(t, s1, one, "docs").Pid
	h.configure(func(log string) map[string]any {
		return map[string]any{"files": stdioEntry(t, log, map[string]any{"timeout": 30})}
	})
	oldID := instanceOf(before, "files").Value("id")
	after := s1.until("servers", func(v delta.JsonValue) bool {
		return len(instanceList(v)) == 1 && isUp("files")(v) && instanceList(v)[0].Value("id") != oldID
	})
	expectEqual(t, stringOf(instanceList(after)[0].Value("server")), "files")
	exited(t, files)
	exited(t, docs)
	if whoami(t, s1, one, "files").Pid == files {
		t.Fatal("the changed server was not restarted")
	}
}

// workerEntryGeneration reports whether a reference entry of the Mcp type is a worker's of generation.
func workerEntryGeneration(entry any, generation int) bool {
	encoded, err := chordjson.Marshal(entry)
	if err != nil {
		return false
	}
	var parsed struct {
		Principal struct {
			Kind       string
			Generation int
		}
	}
	return json.Unmarshal(encoded, &parsed) == nil && parsed.Principal.Kind == "worker" && parsed.Principal.Generation == generation
}

func leaseCwds(h *mcpHost, session string) []string {
	cwds := []string{}
	value, _ := h.manager.Member("leases").Value()
	for _, lease := range instanceList(value) {
		if lease.Value("session") == session {
			cwds = append(cwds, cwdOf(objectOf(lease.Value("location"))))
		}
	}
	return cwds
}

func TestGenerationsOnlyTheRunningCurrentOneSetsAndCallsAnOlderViewsEndLeavesTheNewerLease(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any { return map[string]any{"files": stdioEntry(t, log, nil)} })
	one := h.at("one")
	older := h.worker("s1", 1)
	noError(t, older.setLocations(one))
	instance := instanceList(older.until("servers", isUp("files")))[0]
	pid := whoami(t, older, one, "files").Pid
	// The session's worker restarts: generation 2 runs now.
	newer := h.worker("s1", 2)
	p2test.ExpectChord(t, older.setLocations(one), data.CodeForbidden)
	_, err := older.callTool(one, "files", "echo", nil)
	p2test.ExpectChord(t, err, data.CodeForbidden)
	noError(t, newer.setLocations(one))
	expectEqual(t, instanceList(newer.until("servers", isUp("files")))[0].Value("id"), instance.Value("id"))
	expectEqual(t, whoami(t, newer, one, "files").Pid, pid)
	// The older view really ends (another principal: its own object), and releases only its own generation's leases.
	older.Release()
	_, err = p2test.Until(h.f.Host.References(contract.Mcp), func(value delta.JsonValue) bool {
		list, _ := value.([]any)
		return !slices.ContainsFunc(list, func(entry any) bool { return workerEntryGeneration(entry, 1) })
	}, 0)
	noError(t, err)
	leases, err := p2test.Settled(h.manager.Member("leases"), 0, 0)
	noError(t, err)
	var held [][2]string
	for _, lease := range instanceList(leases) {
		held = append(held, [2]string{stringOf(lease.Value("session")), cwdOf(objectOf(lease.Value("location")))})
	}
	expectJSON(t, held, [][2]string{{"s1", cwdOf(one)}})
	if !alive(pid) {
		t.Fatal("the process ended with the older view")
	}
	expectEqual(t, len(h.starts()), 1)
	// The manager's own rule, decided on its line: of two generations' concurrent calls, the newer one's leases stay.
	caller := func(generation int, locations ...*delta.JsonObject) *delta.JsonObject {
		return delta.JsonObjectOf("session", "s9", "generation", float64(generation), "incarnation", fmt.Sprintf("i%d", generation), "locations", locationsOf(locations))
	}
	var wg sync.WaitGroup
	for _, args := range []*delta.JsonObject{caller(4, one), caller(3, h.at("three"))} {
		wg.Go(func() { _, _ = h.manager.Call("setLocations", args, p2test.Background) })
	}
	wg.Wait()
	expectJSON(t, leaseCwds(h, "s9"), []string{cwdOf(one)})
	_, err = h.manager.Call("setLocations", caller(3, one), p2test.Background)
	p2test.ExpectChord(t, err, data.CodeForbidden)
	_, err = h.manager.Call("setLocations", caller(4), p2test.Background)
	noError(t, err)
}

func TestAWorkerAskingBeforeItsStartIsPublishedWaitsThenItSetsOrANewerGenerationRefusesIt(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any { return map[string]any{"files": stdioEntry(t, log, nil)} })
	one := h.at("one")
	early := h.worker("s1", 1, workerOptions{stale: true})
	first := make(chan error, 1)
	go func() { first <- early.setLocations(one) }()
	if _, err := p2test.Settled(h.manager.Member("leases"), 0, 0); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-first:
		t.Fatalf("the call settled before its worker was published: %v", err)
	default:
	}
	h.running("s1", 1)
	noError(t, <-first)
	early.until("servers", isUp("files"))
	// Another session's worker waits, and the generation published instead is newer.
	late := h.worker("s2", 1, workerOptions{stale: true})
	refused := make(chan error, 1)
	go func() { refused <- late.setLocations(one) }()
	h.running("s2", 2)
	p2test.ExpectChord(t, <-refused, data.CodeForbidden)
	// Releasing the reference ends a waiting call.
	gone := h.worker("s3", 1, workerOptions{stale: true})
	waiting := make(chan error, 1)
	go func() { waiting <- gone.setLocations(one) }()
	gone.Release()
	expectError(t, <-waiting, "")
	leases, err := p2test.Settled(h.manager.Member("leases"), 0, 0)
	noError(t, err)
	sessions := []string{}
	for _, lease := range instanceList(leases) {
		sessions = append(sessions, stringOf(lease.Value("session")))
	}
	expectJSON(t, sessions, []string{"s1"})
}

func TestAnInvalidMcpJSONKeepsTheLastValidServersAlsoAcrossAReload(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any { return map[string]any{"files": stdioEntry(t, log, nil)} })
	one := h.at("one")
	s1 := h.worker("s1", 1)
	noError(t, s1.setLocations(one))
	instance := instanceList(s1.until("servers", isUp("files")))[0]
	pid := whoami(t, s1, one, "files").Pid
	h.write("{ half saved")
	owner := h.owner()
	owner.until("errors", func(v delta.JsonValue) bool {
		return slices.ContainsFunc(errorsText(v), func(line string) bool { return strings.Contains(line, "keeping the last valid servers") })
	})
	noError(t, h.f.ReloadFacets([]nexthost.Facet{ServerFacet(lingerTest, noEnvironmentsConnector)}))
	after := s1.until("servers", func(v delta.JsonValue) bool { return isUp("files")(v) && len(h.starts()) == 2 })
	expectEqual(t, instanceList(after)[0].Value("id"), instance.Value("id"))
	if alive(pid) {
		t.Fatalf("process %d is still alive", pid)
	}
	// The restarted reader reads the half-saved file once more before it reports it.
	owner.until("errors", func(v delta.JsonValue) bool {
		return regexp.MustCompile(`Invalid mcp.json syntax; keeping the last valid servers`).MatchString(strings.Join(errorsText(v), "\n"))
	})
}

func TestAViewOutlivesItsLastReferenceByTheLingerAWorkerRetakingItKeepsItsProcesses(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any { return map[string]any{"files": stdioEntry(t, log, nil)} })
	one := h.at("one")
	h.running("s1", 1)
	// One worker host (one incarnation) whose object lets go of the view and takes it again.
	worker := h.f.Client(p2runtimeWorker("s1", 1))
	hold := func() mview {
		ref, err := p2test.HoldIn(worker, contract.Mcp, delta.NewJsonObject(0), "worker")
		noError(t, err)
		return mview{ref, t}
	}
	first := hold()
	noError(t, first.setLocations(one))
	first.until("servers", isUp("files"))
	pid := whoami(t, first, one, "files").Pid
	first.Release()
	again := hold()
	expectEqual(t, whoami(t, again, one, "files").Pid, pid)
	expectEqual(t, len(h.starts()), 1)
	again.Release()
}

func TestAnotherIncarnationOfTheSameGenerationTakesTheLeaseTheFirstOnesCallsAreRefused(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any { return map[string]any{"files": stdioEntry(t, log, nil)} })
	one := h.at("one")
	// Two hosts with one principal: two incarnations of one worker generation (one view object).
	first := h.worker("s1", 2)
	noError(t, first.setLocations(one))
	first.until("servers", isUp("files"))
	second := h.worker("s1", 2)
	noError(t, second.setLocations(one))
	_, err := first.callTool(one, "files", "echo", echoArgs("x"))
	p2test.ExpectChord(t, err, data.CodeForbidden)
	expectEqual(t, text(t, second, one, "files", "echo", echoArgs("x")), "echo: x")
}

func TestPlacementEnvironmentsNotInEnvsJSONFailHTTPServersConnectWithHostVariablesPerSessionWhenUnsharedOrAskToSignIn(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	var mu sync.Mutex
	var authorizations []string
	sessions := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/locked" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.Method != http.MethodPost {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var message struct {
			ID     *float64 `json:"id"`
			Method string   `json:"method"`
		}
		_ = json.NewDecoder(request.Body).Decode(&message)
		mu.Lock()
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		mu.Unlock()
		if message.ID == nil {
			response.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch message.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "http-fixture", "version": "1.0.0"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "lookup", "inputSchema": map[string]any{"type": "object"}}}}
		default:
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "found"}}}
		}
		header := response.Header()
		header.Set("content-type", "application/json")
		// Each `initialize` opens one MCP session: one per connection of the client.
		if message.Method == "initialize" {
			mu.Lock()
			id := fmt.Sprintf("m%d", len(sessions))
			sessions[request.URL.Path+":"+id] = true
			mu.Unlock()
			header.Set("mcp-session-id", id)
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	t.Setenv("P2_MCP_TEST_TOKEN", "secret-token")
	h := newMcpHost(t, func(log string) map[string]any {
		return map[string]any{
			"here":   stdioEntry(t, log, nil),
			"gpu":    stdioEntry(t, log, map[string]any{"runOn": map[string]any{"env": "gpu"}}),
			"docs":   map[string]any{"url": server.URL + "/mcp", "headers": map[string]any{"Authorization": "Bearer ${P2_MCP_TEST_TOKEN}"}},
			"mine":   map[string]any{"url": server.URL + "/mine", "shared": false},
			"locked": map[string]any{"url": server.URL + "/locked"},
		}
	}, mcpOptions{leaksChecked: true})
	remote := locJSON("remote", "/work")
	s1 := h.worker("s1", 1, workerOptions{env: "remote"})
	s2 := h.worker("s2", 1)
	noError(t, s1.setLocations(remote))
	noError(t, s2.setLocations(h.at("two")))
	servers := s1.until("servers", func(v delta.JsonValue) bool {
		locked := statusOf(v, "locked")
		return len(instanceList(v)) == 5 && isUp("docs")(v) && isUp("mine")(v) && len(locked) == 1 && locked[0] == "sign-in"
	})
	for _, name := range []string{"here", "gpu"} {
		instance := instanceOf(servers, name)
		expectEqual(t, stringOf(instance.Value("status")), "failed")
		// The worker's view leaves the owner's error out.
		if instance.Has("error") {
			t.Fatalf("%s: the worker's view shows %v", name, instance.Value("error"))
		}
	}
	expectJSON(t, instanceOf(servers, "here").Value("runsOn"), map[string]string{"env": "remote"})
	expectJSON(t, instanceOf(servers, "gpu").Value("runsOn"), map[string]string{"env": "gpu"})
	owner := h.owner()
	all := owner.until("instances", both(isUp("mine", 2), isUp("docs")))
	var failures []string
	for _, instance := range instanceList(all) {
		if instance.Value("server") == "here" && locationEnv(instance) == "remote" {
			failures = append(failures, stringOf(instance.Value("error")))
		}
	}
	expectJSON(t, failures, []string{`environment "remote" is not in envs.json`})
	// One shared docs connection for both sessions, one unshared one per session.
	expectEqual(t, len(statusOf(all, "docs")), 1)
	mu.Lock()
	mine := 0
	for key := range sessions {
		if strings.HasPrefix(key, "/mine:") {
			mine++
		}
	}
	token := slices.Contains(authorizations, "Bearer secret-token")
	mu.Unlock()
	expectEqual(t, mine, 2)
	// Only s2's own stdio server runs on this machine; s1's are on environments.
	s2.until("servers", isUp("here"))
	expectEqual(t, len(h.starts()), 1)
	if !token {
		t.Fatalf("no request carried the expanded token: %v", authorizations)
	}
	expectEqual(t, text(t, s1, remote, "docs", "lookup", nil), "found")
	// A call of a server waiting for a sign-in tells the model where the user signs in.
	_, err := s1.callTool(remote, "locked", "lookup", nil)
	expectError(t, err, `needs a sign-in: ask the user to sign in from the MCP panel in p2's sidebar \(or at /mcp/sign-in/locked on p2\)`)
}

func TestAProjectsMcpJSONItsServersRunAtItsLocationForItsSessionItTurnsTheOwnersOnAndOffThereRefusalsAreReported(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any {
		return map[string]any{"files": stdioEntry(t, log, nil), "off": stdioEntry(t, log, map[string]any{"enabled": false})}
	})
	one, two := h.at("one"), h.at("two")
	node, script := fixture(t)
	body, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"lint":   map[string]any{"command": node, "args": []string{script}, "env": map[string]string{"MCP_FIXTURE_RAW": "${HOME}"}},
		"files":  map[string]any{"enabled": false},
		"off":    map[string]any{"enabled": true},
		"remote": map[string]any{"url": "https://example.com/mcp"},
	}})
	noError(t, err)
	s1, s2 := h.worker("s1", 1), h.worker("s2", 1)
	_, err = s1.Call("setLocations", delta.JsonObjectOf("locations", locationsOf([]*delta.JsonObject{one, two}), "projects", []any{
		delta.JsonObjectOf("location", one, "text", string(body)),
		delta.JsonObjectOf("location", two, "error", "permission denied"),
	}), p2test.Background)
	noError(t, err)
	noError(t, s2.setLocations(one))
	servers := s1.until("servers", func(v delta.JsonValue) bool {
		list := instanceList(v)
		return len(list) == 3 && !slices.ContainsFunc(list, func(i *delta.JsonObject) bool { return i.Value("status") != "connected" })
	})
	var rows [][4]any
	for _, instance := range instanceList(servers) {
		rows = append(rows, [4]any{instance.Value("server"), instance.Value("source"), locationCwd(instance), instance.Value("runsOn")})
	}
	expectJSON(t, rows, [][4]any{
		{"files", "owner", cwdOf(two), "host"},
		{"lint", "project", cwdOf(one), "host"},
		{"off", "owner", cwdOf(one), "host"},
	})
	// The project's server runs at its location, its variables as written.
	lint := whoami(t, s1, one, "lint")
	expectEqual(t, lint.Cwd, cwdOf(one))
	expectEqual(t, lint.Vars["MCP_FIXTURE_RAW"], "${HOME}")
	_, err = s1.callTool(one, "files", "echo", nil)
	expectError(t, err, `MCP server "files" is not configured at`)
	errs := errorsText(s1.member("errors"))
	if len(errs) != 2 {
		t.Fatalf("errors: %v", errs)
	}
	joined := strings.Join(errs, "\n")
	expectMatch(t, joined, `: \.pi/mcp\.json: server "remote": HTTP servers are for the owner's mcp\.json`)
	expectMatch(t, joined, `: \.pi/mcp\.json: permission denied`)
	// Another session at the same location without a project: the owner's servers only; the owner sees every error.
	expectJSON(t, serverNames(s2.until("servers", isUp("files"))), []string{"files"})
	owner := h.owner()
	prefixed := []bool{}
	for _, line := range errorsText(owner.until("errors", lenIs(2))) {
		prefixed = append(prefixed, strings.HasPrefix(line, "session s1: "))
	}
	expectJSON(t, prefixed, []bool{true, true})
	// The project file gone: its server ends, the owner's servers are as configured again.
	noError(t, s1.setLocations(one))
	s1.until("servers", func(v delta.JsonValue) bool { return len(instanceList(v)) == 1 && isUp("files")(v) })
	exited(t, lint.Pid)
	s1.until("errors", lenIs(0))
}

func TestALeaseGetsServersOnlyOnEnvironmentsItsSessionWorksOnItsOwnItsSubagentsOwnLocations(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any { return map[string]any{"files": stdioEntry(t, log, nil)} })
	one := h.at("one")
	gpu := locJSON("gpu", "/work")
	s1 := h.worker("s1", 1)
	noError(t, s1.setLocations(one, gpu))
	here := instanceList(s1.until("servers", isUp("files")))[0]
	expectEqual(t, locationCwd(here), cwdOf(one))
	expectJSON(t, errorsText(s1.member("errors")), []string{"gpu:/work: not where this session works"})
	// A subagent working on gpu in its own location: the lease there gets its servers (gpu is not in envs.json).
	h.subagents("s1", []any{delta.JsonObjectOf("name", "own", "conversationId", float64(2), "model", "faux", "thinkingLevel", "off",
		"tools", []any{}, "location", locJSON("gpu", "/elsewhere"), "lastEntryId", float64(0))})
	both2 := s1.until("servers", lenIs(2))
	var pairs []string
	for _, instance := range instanceList(both2) {
		place := locationEnv(instance)
		if place == "" {
			place = "this machine"
		}
		pairs = append(pairs, place+" "+stringOf(instance.Value("status")))
	}
	slices.Sort(pairs)
	expectJSON(t, pairs, []string{"gpu failed", "this machine connected"})
	s1.until("errors", lenIs(0))
	// The subagent gone and the session moved to gpu: this machine is no longer where it works.
	h.subagents("s1", []any{})
	h.move("s1", "gpu")
	moved := s1.until("servers", func(v delta.JsonValue) bool {
		list := instanceList(v)
		return len(list) == 1 && locationEnv(list[0]) == "gpu"
	})
	expectEqual(t, stringOf(instanceList(moved)[0].Value("status")), "failed")
	expectJSON(t, errorsText(s1.member("errors")), []string{cwdOf(one) + ": not where this session works"})
}

func TestEnvironmentsServersStartThroughTheEnvironmentsDaemonOnOneConnectionThatClosesWithTheLastInstance(t *testing.T) {
	directory := t.TempDir()
	daemon := filepath.Join(directory, "pi-env")
	build := exec.Command("go", "build", "-trimpath", "-o", daemon, "github.com/MichaelKinsy/PiG/env/cmd/pi-env")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the daemon: %v\n%s", err, output)
	}
	// The "environment" is this machine through a local daemon: no SSH, the same operations.
	var mu sync.Mutex
	var connections []*env.Connection
	connector := func(nexthost.Self) ConnectEnv {
		return func(string, *chordjson.Object) *env.Connection {
			connection := env.NewConnection(env.ConnectionOptions{Command: []string{daemon}})
			mu.Lock()
			connections = append(connections, connection)
			mu.Unlock()
			return connection
		}
	}
	h := newMcpHost(t, func(log string) map[string]any {
		return map[string]any{"files": stdioEntry(t, log, nil), "tools": stdioEntry(t, log, map[string]any{"runOn": map[string]any{"env": "box"}, "cwd": "~"})}
	}, mcpOptions{envs: delta.JsonObjectOf("box", delta.JsonObjectOf("type", "ssh", "host", "box.invalid")), connector: connector})
	there := locJSON("box", cwdOf(h.at("work")))
	s1 := h.worker("s1", 1, workerOptions{env: "box"})
	noError(t, s1.setLocations(there))
	servers := s1.until("servers", both(isUp("files"), isUp("tools")))
	var runs [][2]any
	for _, instance := range instanceList(servers) {
		runs = append(runs, [2]any{instance.Value("server"), instance.Value("runsOn")})
	}
	expectJSON(t, runs, [][2]any{{"files", map[string]string{"env": "box"}}, {"tools", map[string]string{"env": "box"}}})
	mu.Lock()
	opened := slices.Clone(connections)
	mu.Unlock()
	expectEqual(t, len(opened), 1)
	info, err := opened[0].Info(context.Background())
	noError(t, err)
	// Both run under the one daemon with the location as their root: `files` at the location, `tools` at its cwd `~`,
	// the environment's home.
	files := whoami(t, s1, there, "files")
	tools := whoami(t, s1, there, "tools")
	expectJSON(t, []int{files.Ppid, tools.Ppid}, []int{info.Pid, info.Pid})
	expectEqual(t, files.Cwd, cwdOf(there))
	expectJSON(t, files.Roots, []map[string]string{{"uri": fileURL(cwdOf(there))}})
	home, err := os.UserHomeDir()
	noError(t, err)
	expectEqual(t, tools.Cwd, home)
	expectJSON(t, tools.Roots, files.Roots)
	expectEqual(t, text(t, s1, there, "files", "echo", echoArgs("far")), "echo: far")
	// The last instance there closes the connection: the daemon ends, and the servers with it.
	noError(t, s1.setLocations())
	exited(t, files.Pid)
	exited(t, tools.Pid)
	exited(t, info.Pid)
	mu.Lock()
	expectEqual(t, len(connections), 1)
	mu.Unlock()
}

func TestAReloadOfThePluginKeepsTheLeasesAndTheToolsTheNewSupervisorStartsWhatTheyUse(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any { return map[string]any{"files": stdioEntry(t, log, nil)} })
	one := h.at("one")
	s1 := h.worker("s1", 1)
	noError(t, s1.setLocations(one))
	before := instanceList(s1.until("servers", isUp("files")))[0]
	var mu sync.Mutex
	fewest := len(instanceTools(before))
	watch, err := s1.Member("servers").Watch(p2test.Background)
	noError(t, err)
	record := func(value delta.JsonValue) {
		count := 0
		if list := instanceList(value); len(list) > 0 {
			count = len(instanceTools(list[0]))
		}
		mu.Lock()
		fewest = min(fewest, count)
		mu.Unlock()
	}
	record(watch.Value())
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			record(call.Value)
		}
		return nil
	})
	// The manager's memento of the listed tools is written a moment after the list changed: let it catch up.
	_, err = p2test.Settled(h.manager.Member("instances"), 0, 0)
	noError(t, err)
	// A new facet value: a reload, not the same code again.
	noError(t, h.f.ReloadFacets([]nexthost.Facet{ServerFacet(lingerTest, noEnvironmentsConnector)}))
	after := instanceList(s1.until("servers", func(v delta.JsonValue) bool { return isUp("files")(v) && len(h.starts()) == 2 }))[0]
	_, _ = watch.Stop()
	expectEqual(t, after.Value("id"), before.Value("id"))
	mu.Lock()
	expectEqual(t, fewest, 4)
	mu.Unlock()
	expectEqual(t, text(t, s1, one, "files", "echo", echoArgs("again")), "echo: again")
}

func TestGuestsGetNoMCPServers(t *testing.T) {
	h := newMcpHost(t, func(log string) map[string]any { return map[string]any{"files": stdioEntry(t, log, nil)} })
	ref, err := h.f.Client(guestPrincipal).Hold(p2test.Background, contract.Mcp, delta.NewJsonObject(0), nexthost.GetOptions{})
	noError(t, err)
	defer ref.Release()
	failure, err := p2test.FailureOf(ref)
	noError(t, err)
	p2test.ExpectChord(t, failure, data.CodeForbidden)
}

var _ = time.Second

func TestAToolCallInTheManagerDoesNotHoldTheHostsLoopAndLeaseChangesRacingTheHostsEndReadItsStateInASection(t *testing.T) {
	t.Run("a tool call that waits for its server lets another call through", func(t *testing.T) {
		h := newMcpHost(t, func(log string) map[string]any { return map[string]any{"files": stdioEntry(t, log, nil)} })
		one := h.at("one")
		s1 := h.worker("s1", 1)
		noError(t, s1.setLocations(one))
		s1.until("servers", isUp("files"))
		gate := filepath.Join(t.TempDir(), "gate")
		release := sync.OnceFunc(func() { noError(t, os.WriteFile(gate, nil, 0o644)) })
		defer release()
		slow := make(chan error, 1)
		go func() {
			_, err := s1.callTool(one, "files", "gated", delta.JsonObjectOf("gate", gate))
			slow <- err
		}()
		if err := p2test.Poll(func() (bool, error) { _, err := os.Stat(gate + ".entered"); return err == nil, nil }, 10*time.Second, "the server to hold the call"); err != nil {
			t.Fatal(err)
		}
		fast := make(chan error, 1)
		go func() {
			text, err := s1.callTool(one, "files", "echo", delta.JsonObjectOf("text", "hi"))
			if err == nil && text != "echo: hi" {
				err = fmt.Errorf("echo answered %q", text)
			}
			fast <- err
		}()
		select {
		case err := <-fast:
			noError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("a call waits for a tool of another call: the manager's loop is held")
		}
		release()
		noError(t, <-slow)
	})
	t.Run("setLocations and releaseLeases answered while the host closes", func(t *testing.T) {
		for round := 0; round < 20; round++ {
			h := newMcpHost(t, func(log string) map[string]any { return map[string]any{"files": stdioEntry(t, log, nil)} })
			one := h.at("one")
			var calls sync.WaitGroup
			for caller := 0; caller < 8; caller++ {
				session := fmt.Sprintf("s%d", caller)
				calls.Add(1)
				go func() {
					defer calls.Done()
					for generation := 1; generation <= 4; generation++ {
						_, _ = h.manager.Call("setLocations", delta.JsonObjectOf("session", session, "generation", float64(generation), "incarnation", "i", "locations", []any{one}), p2test.Background)
					}
					_, _ = h.manager.Call("releaseLeases", delta.JsonObjectOf("session", session, "generation", float64(4)), p2test.Background)
				}()
			}
			// The host ends as the first lease commits: the callers that committed read the placement for their reconcile at once.
			if _, err := p2test.Until(h.manager.Member("leases"), func(value delta.JsonValue) bool { return len(value.([]any)) > 0 }, 0); err != nil {
				t.Fatal(err)
			}
			h.shutdown()
			calls.Wait()
		}
	})
}
