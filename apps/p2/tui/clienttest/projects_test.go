package clienttest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	envscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	projectscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/projects/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sessionstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/tui"
	statustui "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

const (
	left  = "\x1b[D"
	right = "\x1b[C"
)

const projectsCwd = "/home/me/projects/pi/pi/apps"

func projectsSession() contract.Session {
	session := fixtureSession()
	session.Cwd = projectsCwd
	return session
}

func projectJSON(name string, places ...[2]any) *delta.JsonObject {
	list := make([]any, len(places))
	for i, place := range places {
		list[i] = delta.JsonObjectOf("env", place[0], "path", place[1])
	}
	return delta.JsonObjectOf("name", name, "places", list)
}

func projectList() []any {
	return []any{
		projectJSON("pi", [2]any{"mac", "~/projects/pi/pi"}, [2]any{nil, "~/projects/pi/pi"}),
		projectJSON("dead", [2]any{"gpu", "/srv/dead"}),
		projectJSON("tri", [2]any{"mac", "~/missing"}, [2]any{"nope", "~/tri"}),
	}
}

// projectsRows are the other rows of the catalog: one more in pi, one of no project, one of a project projects.json no longer has.
func projectsRows() []contract.Session {
	base := projectsSession()
	second, free, old := base, base, base
	second.ID, second.Title, second.Cwd, second.UpdatedAt = "s2", "Second pi", "/home/me/projects/pi/pi", 4
	free.ID, free.Title, free.Cwd, free.UpdatedAt = "free", "Free work", "/tmp/free", 3
	old.ID, old.Title, old.Cwd, old.UpdatedAt = "old", "Old one", "/srv/old", 2
	return []contract.Session{base, second, free, old}
}

func membersJSON(members map[string]string) *delta.JsonObject {
	object := delta.NewJsonObject(len(members))
	for _, key := range []string{"s", "s2", "old"} {
		if value, ok := members[key]; ok {
			object.Set(key, value)
		}
	}
	for key, value := range members {
		if !object.Has(key) {
			object.Set(key, value)
		}
	}
	return object
}

// calls records what the stubs were called with.
type calls struct {
	mu   sync.Mutex
	data map[string][]any
}

func (c *calls) record(name string, args any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil {
		c.data = map[string][]any{}
	}
	c.data[name] = append(c.data[name], args)
}

func (c *calls) get(name string) []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]any(nil), c.data[name]...)
}

func (c *calls) count(name string) int { return len(c.get(name)) }

// location is a {env, cwd} a create or relocate was called with.
type location struct {
	env any
	cwd string
}

func locationOf(args any) location {
	object := args.(*delta.JsonObject)
	return location{env: object.Value("env"), cwd: object.Value("cwd").(string)}
}

type projectsSetup struct {
	*tuitest.Topology
	calls   *calls
	openMac func()
	mu      sync.Mutex
	down    map[string]bool
	members *p2test.Cell
	projs   *p2test.Cell
}

func (s *projectsSetup) setDown(env string, isDown bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down[env] = isDown
}

func (s *projectsSetup) isDown(env string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.down[env]
}

func noop(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }

func unused(delta.JsonValue, context.Context) (delta.JsonValue, error) {
	return nil, errors.New("not in this test")
}

// newProjectsSetup is the owner stubs: `mac` answers once openMac ran and has no `missing` directory; the machines in down (gpu at first) are unreachable;
// `nope` is not configured; `~` is each machine's home. The errors are the ones `Envs.listDirectory` gives (the SSH manager, the pi-env daemon).
func newProjectsSetup(t *testing.T, facets ...host.Facet) *projectsSetup {
	t.Helper()
	s := &projectsSetup{calls: &calls{}, down: map[string]bool{"gpu": true}}
	macGate := make(chan struct{})
	var once sync.Once
	s.openMac = func() { once.Do(func() { close(macGate) }) }
	homes := map[string]string{"": "/home/me", "mac": "/Users/me"}
	sessions := p2test.Stub{Contract: contract.Sessions, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		rows := self.Cell(rowsOf(projectsRows()...))
		return p2test.StubMembers{
			Variant: "owner",
			State:   map[string]any{"sessions": rows, "defaultCwd": "~", "defaultEnv": nil, "operations": []any{}},
			Methods: map[string]p2test.StubMethod{
				"create": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					s.calls.record("create", location{env: object.Value("env"), cwd: object.Value("cwd").(string)})
					created := projectsSession()
					created.ID, created.Cwd = object.Value("id").(string), object.Value("cwd").(string)
					if env, ok := object.Value("env").(string); ok {
						created.Env = &env
					}
					_ = rows.Change(func(current any) any { return append(append([]any{}, current.([]any)...), created.JSON()) })
					return created.JSON(), nil
				},
				"relocate": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					s.calls.record("relocate", locationOf(object))
					id := object.Value("id")
					_ = rows.Change(func(current any) any {
						var out []any
						for _, each := range current.([]any) {
							row := each.(*delta.JsonObject).Clone()
							if row.Value("id") == id {
								row.Set("cwd", object.Value("cwd"))
								row.Set("env", object.Value("env"))
							}
							out = append(out, row)
						}
						return out
					})
					return nil, nil
				},
				"rename": noop, "pin": noop, "archive": noop, "restore": noop, "dismissOperation": noop, "clone": unused, "fork": unused,
			},
		}
	}}
	envs := p2test.Stub{Contract: envscontract.Envs, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		var list []any
		for _, name := range []string{"mac", "gpu"} {
			list = append(list, delta.JsonObjectOf("name", name, "config", delta.JsonObjectOf("type", "ssh", "host", name+".example"), "sessions", float64(0)))
		}
		methods := map[string]p2test.StubMethod{
			"warm": noop,
			"list": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return []any{}, nil },
			"listDirectory": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := args.(*delta.JsonObject)
				env, _ := object.Value("env").(string)
				path := object.Value("path").(string)
				s.calls.record("list", env+":"+path)
				if s.isDown(env) {
					return nil, errors.New("connect ECONNREFUSED")
				}
				if env == "mac" {
					chordctx.Suspend(ctx)
					<-macGate
				}
				if env == "nope" {
					return nil, errors.New(`Environment "nope" is not configured`)
				}
				absolute := path
				if strings.HasPrefix(path, "~") {
					absolute = homes[env] + path[1:]
				}
				if env == "mac" && strings.HasSuffix(absolute, "/missing") {
					return nil, fmt.Errorf("ENOENT: no such file or directory, opendir '%s'", absolute)
				}
				return delta.JsonObjectOf("path", absolute, "parent", nil, "directories", []any{}, "truncated", false), nil
			},
		}
		for _, name := range []string{"save", "delete", "test", "acceptHostKey", "forgetHostKey", "generateKey", "uploadKey", "publicKey", "sandboxStatus", "sandboxStart", "sandboxStop", "sandboxResetDisk"} {
			methods[name] = unused
		}
		return p2test.StubMembers{Variant: "owner", State: map[string]any{"envs": list, "failure": nil}, Methods: methods}
	}}
	projects := p2test.Stub{Contract: projectscontract.Projects, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		s.members = self.Cell(membersJSON(map[string]string{"s": "pi", "s2": "pi", "old": "gone"}))
		s.projs = self.Cell(projectList())
		members := s.members
		return p2test.StubMembers{
			Variant: "owner",
			State:   map[string]any{"projects": s.projs, "failure": nil, "local": "test-host", "sessions": members},
			Methods: map[string]p2test.StubMethod{
				"assign": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					s.calls.record("assign", [2]string{object.Value("session").(string), object.Value("project").(string)})
					_ = members.Change(func(current any) any {
						next := current.(*delta.JsonObject).Clone()
						next.Set(object.Value("session").(string), object.Value("project"))
						return next
					})
					return nil, nil
				},
			},
		}
	}}
	s.Topology = tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: []p2test.Stub{sessions, envs, projects}, Worker: []p2test.Stub{chatWorker()}, Facets: append([]host.Facet{tui.CoreFacet, sessionstui.Facet}, facets...), Columns: 100,
	})
	// A probe left waiting would hold the teardown: the cleanups run last-in first, so mac answers before the host closes.
	t.Cleanup(s.openMac)
	return s
}

// selectRow moves the selection down until the selected row matches row.
func (s *projectsSetup) selectRow(t *testing.T, row string) {
	t.Helper()
	match := matches(row)
	tuitest.Poll(t, func() bool {
		if match(s.Screen()) {
			return true
		}
		s.Send(down)
		return false
	})
}

func (s *projectsSetup) screenIs(pattern string) func(string) bool { return matches(pattern) }

func (s *projectsSetup) showSelector(session bool) {
	s.Do(func() {
		if session {
			s.Runtime.Navigate("sessions", "s", true, nil)
		} else {
			s.Runtime.NavigateScreen("sessions")
		}
	})
}

func (s *projectsSetup) screenName() string {
	name := ""
	s.Do(func() { name = s.Runtime.Screen })
	return name
}

func (s *projectsSetup) session() string {
	id := ""
	s.Do(func() { id = s.Runtime.Session })
	return id
}

func sleep() { time.Sleep(50 * time.Millisecond) }

func TestProjectRowsPlacesProbedOnShowingTheSelectorEnterTakesTheFirstReachablePlaceLeftRightAnother(t *testing.T) {
	s := newProjectsSetup(t)
	s.showSelector(false)
	// mac is still checking, this device and gpu answered.
	pollScreen(t, s.Fixture, matches(`› pi {3}mac … {3}test-host ✓`))
	pollScreen(t, s.Fixture, matches(`dead {3}gpu ✗`))
	// The selected row says why a place cannot be used (none yet: mac is checking, this device answered).
	if matches(`test-host:`)(s.Screen()) {
		t.Fatal("a reason shows for a place that is usable")
	}
	if !matches(`\+ New session`)(s.Screen()) {
		t.Fatal("no + New session row")
	}
	probes := s.calls.count("list")
	// Enter waits for mac (preferred), then creates there at its resolved path.
	s.Send(enter)
	pollContains(t, s.Fixture, "Checking pi's places…")
	s.openMac()
	tuitest.Poll(t, func() bool { return s.calls.count("create") == 1 })
	if got := s.calls.get("create")[0]; got != (location{env: "mac", cwd: "/Users/me/projects/pi/pi"}) {
		t.Fatalf("create = %+v", got)
	}
	if s.screenName() != "chat" {
		t.Fatalf("screen = %s", s.screenName())
	}
	// The new session belongs to the project, recorded before its row exists.
	assigned := s.calls.get("assign")
	if len(assigned) != 1 || assigned[0] != [2]string{s.session(), "pi"} {
		t.Fatalf("assign = %v", assigned)
	}
	// Showing the selector again probes every place again; right picks this device.
	s.showSelector(true)
	tuitest.Poll(t, func() bool { return s.calls.count("list") >= probes+5 })
	pollScreen(t, s.Fixture, matches(`› pi {3}mac ✓ {3}test-host ✓`))
	s.Send(right)
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.calls.count("create") == 2 })
	if got := s.calls.get("create")[1]; got != (location{env: nil, cwd: "/home/me/projects/pi/pi"}) {
		t.Fatalf("create = %+v", got)
	}
	// Showing the selector again forgets the pick: left twice from mac comes back to mac.
	s.showSelector(true)
	pollScreen(t, s.Fixture, matches(`› pi {3}mac ✓ {3}test-host ✓`))
	s.Send(left)
	s.Send(left)
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.calls.count("create") == 3 })
	if got := s.calls.get("create")[2]; got != (location{env: "mac", cwd: "/Users/me/projects/pi/pi"}) {
		t.Fatalf("create = %+v", got)
	}
	// No reachable place: the reason under the row, and on Enter; no session.
	s.showSelector(true)
	pollScreen(t, s.Fixture, matches(`› pi`))
	s.selectRow(t, `› dead`)
	pollScreen(t, s.Fixture, matches(`› dead {3}gpu ✗ *\n {4}gpu: connect ECONNREFUSED`))
	s.Send(enter)
	pollContains(t, s.Fixture, "No reachable place: gpu: connect ECONNREFUSED")
	if s.calls.count("create") != 3 {
		t.Fatalf("creates = %d", s.calls.count("create"))
	}
	// A machine without the folder (?) and an environment that does not exist (✗) read differently.
	s.selectRow(t, `› tri`)
	pollScreen(t, s.Fixture, matches(`› tri {3}mac \? {3}nope ✗ *\n {4}mac: no folder ~/missing · nope: no such environment`))
	// The grouped list has no scope: Tab does nothing.
	s.Send(tab)
	sleep()
	screen := s.Screen()
	if !matches(`› tri`)(screen) || matches(`Resume Session \(|Current Folder|◉`)(screen) {
		t.Fatalf("Tab changed the grouped list:\n%s", screen)
	}
}

func TestGroupedByProjectEverySessionUnderItsProjectThenOtherNoScopeASearchIsFlatWithTabsScope(t *testing.T) {
	s := newProjectsSetup(t)
	// A probe left waiting would hold the teardown: mac answers at once here.
	s.openMac()
	s.showSelector(false)
	// Every project with its sessions (newest first), then Other: the sessions of no project and the one whose project is gone, dimmed with its old name.
	pollScreen(t, s.Fixture, matches(`› pi {3}mac ✓ {3}test-host ✓ *\n {6}Second pi.*\n {6}Fixture.*\n {2}dead {3}gpu ✗ *\n {2}tri .*\n {2}Other *\n {6}\+ New session *\n {6}Free work.*\n {6}Old one \(gone\)`))
	// No scope in the header or the hints, and Tab changes nothing.
	screen := s.Screen()
	if !matches(`(?m)^Resume Session {2}`)(screen) || matches(`Current Folder|◉|tab scope`)(screen) {
		t.Fatalf("scope shows in the grouped list:\n%s", screen)
	}
	s.Send(tab)
	sleep()
	if after := s.Screen(); after != screen {
		t.Fatalf("Tab changed the screen:\n%s\n---\n%s", screen, after)
	}
	// The project comes back: its session groups again.
	_ = s.projs.Change(func(current any) any {
		return append(append([]any{}, current.([]any)...), projectJSON("gone", [2]any{nil, "/srv/old"}))
	})
	pollScreen(t, s.Fixture, matches(`gone {3}test-host .*\n {6}Old one {2}`))
	// A search lists flat, without project rows or groups, over every session; Tab narrows it to the current folder.
	typed(s.Fixture, "work")
	pollScreen(t, s.Fixture, matches(`Resume Session \(All\)[\s\S]*› +Free work`))
	if matches(`Other|\+ New session|test-host`)(s.Screen()) {
		t.Fatalf("a search shows groups:\n%s", s.Screen())
	}
	s.Send(tab)
	pollContains(t, s.Fixture, "Resume Session (Current Folder)")
	if matches(`Free work|Other|\+ New session|test-host`)(s.Screen()) {
		t.Fatalf("the current folder shows other rows:\n%s", s.Screen())
	}
	// Back to the grouped list: no scope again.
	s.Send("\x1b")
	pollScreen(t, s.Fixture, matches(`(?m)^Resume Session {2}`))
	if !matches(` {2}Other *\n {6}\+ New session *\n {6}Free work`)(s.Screen()) {
		t.Fatalf("the grouped list did not come back:\n%s", s.Screen())
	}
}

func TestAFailedListingReadsAsNoFolderNoSuchEnvironmentOrUnreachableFromItsMessage(t *testing.T) {
	mac := "mac"
	place := projectscontract.ProjectPlace{Env: &mac, Path: "~/x"}
	// The messages Envs.listDirectory gives: this device (Node), an SSH environment (the pi-env daemon, the manager).
	for _, message := range []string{
		"ENOENT: no such file or directory, scandir '/home/me/x'",
		"ENOTDIR: not a directory, scandir '/home/me/x'",
		"ENOENT: no such file or directory, opendir '/Users/me/x'",
		"Working directory is not a directory",
	} {
		if got := sessionstui.PlaceFailure(place, errors.New(message)); got != (sessionstui.PlaceState{Kind: sessionstui.PlaceMissing, Reason: "no folder ~/x"}) {
			t.Fatalf("%q: %+v", message, got)
		}
	}
	if got := sessionstui.PlaceFailure(place, errors.New(`Environment "mac" is not configured`)); got != (sessionstui.PlaceState{Kind: sessionstui.PlaceUnreachable, Reason: "no such environment"}) {
		t.Fatalf("not configured: %+v", got)
	}
	refused := "ssh 127.0.0.1 failed with exit code 255: ssh: connect to host 127.0.0.1 port 9: Connection refused"
	if got := sessionstui.PlaceFailure(place, errors.New(refused)); got != (sessionstui.PlaceState{Kind: sessionstui.PlaceUnreachable, Reason: refused}) {
		t.Fatalf("refused: %+v", got)
	}
}

// newMoveSetup is the setup of the /move tests: the chat and the footer on the session at the project's folder.
func newMoveSetup(t *testing.T) *projectsSetup {
	t.Helper()
	s := newProjectsSetup(t, chattui.Facet, statustui.Facet)
	s.Do(func() { s.Runtime.Navigate("chat", "s", true, nil) })
	pollContains(t, s.Fixture, projectsCwd+" • Fixture")
	return s
}

func (s *projectsSetup) draft() string {
	text := ""
	s.Do(func() { text = s.Runtime.Drafts["s#1"] })
	return text
}

func (s *projectsSetup) noticed(text string) func() bool {
	return func() bool {
		found := false
		s.Do(func() {
			for _, notice := range s.Runtime.Notices {
				if notice.Text == text {
					found = true
				}
			}
		})
		return found
	}
}

func (s *projectsSetup) relocateCount() int { return s.calls.count("relocate") }

func (s *projectsSetup) relocated(index int) location {
	return s.calls.get("relocate")[index].(location)
}

func (s *projectsSetup) surface() *sessionstui.Surface {
	var surface *sessionstui.Surface
	s.Do(func() { surface = s.Runtime.Ctx.Surface("sessions").(*sessionstui.Surface) })
	return surface
}

func (s *projectsSetup) projectOf(id string) (name string, ok bool) {
	s.Do(func() { name, ok = s.surface().ProjectOf(id) })
	return name, ok
}

func TestMoveEnvFollowsTheStoredProjectKeepsTheSubfolderWorksWhileTheSessionsMachineIsDown(t *testing.T) {
	s := newMoveSetup(t)
	s.openMac()
	// Completion lists the project's places on other machines.
	typed(s.Fixture, "/move m")
	s.Send(tab)
	tuitest.Poll(t, func() bool { return s.draft() == "/move mac" })
	s.Send(enter)
	pollContains(t, s.Fixture, "Move this session to mac:/Users/me/projects/pi/pi/apps?")
	typed(s.Fixture, "y")
	tuitest.Poll(t, func() bool { return s.relocateCount() == 1 })
	if got := s.relocated(0); got != (location{env: "mac", cwd: "/Users/me/projects/pi/pi/apps"}) {
		t.Fatalf("relocate = %+v", got)
	}
	pollContains(t, s.Fixture, "mac:/Users/me/projects/pi/pi/apps • Fixture")
	// The Mac goes away: back by this device's label, without asking the Mac anything.
	s.setDown("mac", true)
	listed := s.calls.count("list")
	typed(s.Fixture, "/move test-host")
	s.Send(enter)
	pollContains(t, s.Fixture, "Move this session to /home/me/projects/pi/pi/apps?")
	typed(s.Fixture, "y")
	tuitest.Poll(t, func() bool { return s.relocateCount() == 2 })
	if got := s.relocated(1); got != (location{env: nil, cwd: "/home/me/projects/pi/pi/apps"}) {
		t.Fatalf("relocate = %+v", got)
	}
	for _, each := range s.calls.get("list")[listed:] {
		if strings.HasPrefix(fmt.Sprint(each), "mac:") {
			t.Fatalf("the Mac was asked: %v", each)
		}
	}
	pollScreen(t, s.Fixture, matches(`(?m)^/home/me/projects/pi/pi/apps • Fixture`))
	for _, each := range [][2]string{
		{"/move gpu", "Project pi has no place on gpu"},
		{"/move mac", "mac is unreachable: connect ECONNREFUSED"},
		{"/move local", "The session is already at /home/me/projects/pi/pi/apps"},
	} {
		typed(s.Fixture, each[0])
		s.Send(enter)
		tuitest.Poll(t, s.noticed(each[1]))
	}
	// A subfolder the target does not have: the target's root, and the notice says so.
	s.setDown("mac", false)
	typed(s.Fixture, "/cd /home/me/projects/pi/pi/missing")
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.relocateCount() == 3 })
	typed(s.Fixture, "/move mac")
	s.Send(enter)
	tuitest.Poll(t, s.noticed("missing does not exist on mac; moving to the project's root"))
	pollContains(t, s.Fixture, "Move this session to mac:/Users/me/projects/pi/pi?")
	typed(s.Fixture, "y")
	tuitest.Poll(t, func() bool { return s.relocateCount() == 4 })
	if got := s.relocated(3); got != (location{env: "mac", cwd: "/Users/me/projects/pi/pi"}) {
		t.Fatalf("relocate = %+v", got)
	}
	// `/cd` outside the project's place keeps the project: `/move` goes to the target place's root.
	typed(s.Fixture, "/cd /tmp")
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.relocateCount() == 5 })
	typed(s.Fixture, "/move test-host")
	s.Send(enter)
	pollContains(t, s.Fixture, "Move this session to /home/me/projects/pi/pi?")
	typed(s.Fixture, "y")
	tuitest.Poll(t, func() bool { return s.relocateCount() == 6 })
	if got := s.relocated(5); got != (location{env: nil, cwd: "/home/me/projects/pi/pi"}) {
		t.Fatalf("relocate = %+v", got)
	}
	// No project, or one projects.json no longer has: nothing moves.
	for _, each := range []struct {
		membership map[string]string
		notice     string
	}{
		{map[string]string{}, "This session belongs to no project: start one from a project's row in /sessions"},
		{map[string]string{"s": "gone"}, "Project gone is no longer in projects.json"},
	} {
		_ = s.members.Replace(membersJSON(each.membership))
		want, has := each.membership["s"]
		tuitest.Poll(t, func() bool {
			name, ok := s.projectOf("s")
			return ok == has && name == want
		})
		typed(s.Fixture, "/move mac")
		s.Send(enter)
		tuitest.Poll(t, s.noticed(each.notice))
	}
	if s.relocateCount() != 6 {
		t.Fatalf("relocates = %d", s.relocateCount())
	}
	if s.calls.count("assign") != 0 {
		t.Fatal("a project was assigned")
	}
}

func TestMoveWithoutAnArgumentAPickerOfTheProjectsPlacesTheCurrentOneMarkedOnlyReachableOnesMove(t *testing.T) {
	s := newMoveSetup(t)
	s.openMac()
	_ = s.projs.Replace([]any{projectJSON("pi", [2]any{"gpu", "~/projects/pi/pi"}, [2]any{nil, "~/projects/pi/pi"}, [2]any{"mac", "~/projects/pi/pi"})})
	tuitest.Poll(t, func() bool {
		places := 0
		s.Do(func() {
			if projects := s.surface().Projects(); len(projects) > 0 {
				places = len(projects[0].Places)
			}
		})
		return places == 3
	})
	typed(s.Fixture, "/move")
	s.Send(enter)
	// Every place in projects.json's order, this device marked current; the cursor skips gpu (unreachable) for mac.
	pollScreen(t, s.Fixture, matches(` {2}✗ gpu {2}~/projects/pi/pi +connect ECONNREFUSED *\n {2}• test-host {2}~/projects/pi/pi +current *\n→ ✓ mac {2}~/projects/pi/pi`))
	if !matches(`Move within pi`)(s.Screen()) {
		t.Fatal("no picker title")
	}
	// An unreachable place cannot be chosen.
	s.Send(up)
	s.Send(up)
	s.Send(enter)
	pollContains(t, s.Fixture, "gpu cannot be chosen: only ✓ places are reachable")
	// The current place only closes the picker.
	s.Send(down)
	s.Send(enter)
	tuitest.Poll(t, s.noticed("Already here"))
	pollScreen(t, s.Fixture, func(screen string) bool { return !strings.Contains(screen, "Move within pi") })
	if s.relocateCount() != 0 {
		t.Fatal("the current place moved the session")
	}
	// Escape cancels.
	typed(s.Fixture, "/move")
	s.Send(enter)
	pollScreen(t, s.Fixture, matches(`→ ✓ mac`))
	s.Send("\x1b")
	pollScreen(t, s.Fixture, func(screen string) bool { return !strings.Contains(screen, "Move within pi") })
	if s.relocateCount() != 0 {
		t.Fatal("Escape moved the session")
	}
	// Enter on a reachable place is `/move <it>`: the subfolder kept, the move confirmed.
	typed(s.Fixture, "/move")
	s.Send(enter)
	pollScreen(t, s.Fixture, matches(`→ ✓ mac`))
	s.Send(enter)
	pollContains(t, s.Fixture, "Move this session to mac:/Users/me/projects/pi/pi/apps?")
	typed(s.Fixture, "y")
	tuitest.Poll(t, func() bool { return s.relocateCount() == 1 })
	if got := s.relocated(0); got != (location{env: "mac", cwd: "/Users/me/projects/pi/pi/apps"}) {
		t.Fatalf("relocate = %+v", got)
	}
	// Only the current place, or no project: today's errors, no picker.
	_ = s.projs.Replace([]any{projectJSON("pi", [2]any{"mac", "~/projects/pi/pi"})})
	tuitest.Poll(t, func() bool {
		places := 0
		s.Do(func() {
			if projects := s.surface().Projects(); len(projects) > 0 {
				places = len(projects[0].Places)
			}
		})
		return places == 1
	})
	pollContains(t, s.Fixture, "mac:/Users/me/projects/pi/pi/apps • Fixture")
	typed(s.Fixture, "/move")
	s.Send(enter)
	tuitest.Poll(t, s.noticed("Project pi has no place on another machine"))
	_ = s.members.Replace(membersJSON(map[string]string{}))
	tuitest.Poll(t, func() bool { _, ok := s.projectOf("s"); return !ok })
	typed(s.Fixture, "/move")
	s.Send(enter)
	tuitest.Poll(t, s.noticed("This session belongs to no project: start one from a project's row in /sessions"))
	if strings.Contains(s.Screen(), "Move within") {
		t.Fatal("a picker shows")
	}
}
