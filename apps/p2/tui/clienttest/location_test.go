package clienttest

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	envscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	envstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sessionstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/tui"
	statustui "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

type dirEntry struct {
	path        string
	directories []string
}

// locationTree is the directories per (env, path) of the fixture; `~` is each machine's home.
var locationTree = map[string]dirEntry{
	"\x00/work":     {"/work", []string{"/work/docs", "/work/src", "/work/.git"}},
	"\x00/work/src": {"/work/src", []string{"/work/src/lib"}},
	"\x00~":         {"/home/me", nil},
	"gpu\x00~":      {"/home/gpu", []string{"/home/gpu/data"}},
	"gpu\x00/data":  {"/data", nil},
	"gpu\x00/work":  {"/work", nil},
}

type locationSetup struct {
	*tuitest.Topology
	calls *calls
}

func newLocationSetup(t *testing.T, role string) *locationSetup {
	t.Helper()
	s := &locationSetup{calls: &calls{}}
	sessions := p2test.Stub{Contract: contract.Sessions, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		session := fixtureSession()
		rows := self.Cell(rowsOf(session))
		if role == "guest" {
			return p2test.StubMembers{Variant: "guest", State: map[string]any{"sessions": rows}}
		}
		return p2test.StubMembers{
			Variant: "owner",
			State:   map[string]any{"sessions": rows, "defaultCwd": "/work", "defaultEnv": nil, "operations": []any{}},
			Methods: map[string]p2test.StubMethod{
				"create": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					s.calls.record("create", location{env: object.Value("env"), cwd: object.Value("cwd").(string)})
					created := session
					created.ID, created.Cwd = object.Value("id").(string), object.Value("cwd").(string)
					if env, ok := object.Value("env").(string); ok {
						created.Env = &env
					}
					_ = rows.Change(func(current any) any { return append(append([]any{}, current.([]any)...), created.JSON()) })
					return created.JSON(), nil
				},
				"relocate": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					s.calls.record("relocate", [3]any{object.Value("id"), object.Value("env"), object.Value("cwd")})
					_ = rows.Change(func(current any) any {
						var out []any
						for _, each := range current.([]any) {
							row := each.(*delta.JsonObject).Clone()
							if row.Value("id") == object.Value("id") {
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
	envs := p2test.Stub{Contract: envscontract.Envs, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		s.calls.record("envsAcquired", true)
		list := []any{delta.JsonObjectOf("name", "gpu", "config", delta.JsonObjectOf("type", "ssh", "host", "gpu.example"), "sessions", float64(0))}
		methods := map[string]p2test.StubMethod{
			"warm": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				s.calls.record("warm", true)
				return nil, nil
			},
			"list": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return []any{}, nil },
			"listDirectory": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				object := args.(*delta.JsonObject)
				env, _ := object.Value("env").(string)
				path := object.Value("path").(string)
				s.calls.record("list", env+":"+path)
				found, ok := locationTree[env+"\x00"+path]
				if !ok {
					return nil, fmt.Errorf("ENOENT: no such file or directory, scandir '%s'", path)
				}
				parent := "/"
				if index := strings.LastIndex(found.path, "/"); index > 0 {
					parent = found.path[:index]
				}
				directories := make([]any, len(found.directories))
				for i, each := range found.directories {
					directories[i] = each
				}
				return delta.JsonObjectOf("path", found.path, "parent", parent, "directories", directories, "truncated", false), nil
			},
		}
		for _, name := range []string{"save", "delete", "test", "acceptHostKey", "forgetHostKey", "generateKey", "uploadKey", "publicKey", "sandboxStatus", "sandboxStart", "sandboxStop", "sandboxResetDisk"} {
			methods[name] = unused
		}
		return p2test.StubMembers{Variant: "owner", State: map[string]any{"envs": list, "failure": nil}, Methods: methods}
	}}
	s.Topology = tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: []p2test.Stub{sessions, envs}, Worker: []p2test.Stub{chatWorker()},
		Facets:  []host.Facet{tui.CoreFacet, chattui.Facet, sessionstui.Facet, statustui.Facet, envstui.Facet},
		Columns: 100, Role: roleOf(role),
	})
	s.Do(func() { s.Runtime.Navigate("chat", "s", true, nil) })
	pollContains(t, s.Fixture, "/work • Fixture")
	return s
}

func (s *locationSetup) draft() string {
	text := ""
	s.Do(func() { text = s.Runtime.Drafts["s#1"] })
	return text
}

func (s *locationSetup) hasNotice(text string, prefix bool) func() bool {
	return func() bool {
		found := false
		s.Do(func() {
			for _, notice := range s.Runtime.Notices {
				if notice.Text == text || (prefix && strings.HasPrefix(notice.Text, text)) {
					found = true
				}
			}
		})
		return found
	}
}

func (s *locationSetup) relocates() [][3]any {
	var out [][3]any
	for _, each := range s.calls.get("relocate") {
		out = append(out, each.([3]any))
	}
	return out
}

func TestCdRelocatesFolderCompletionRelativePathsTheWebsErrorsEnvMovesConfirmedFooterFollows(t *testing.T) {
	s := newLocationSetup(t, "owner")
	// Tab completes folders of the session's directory (`s` → `src/`; `.git` would need a typed dot).
	typed(s.Fixture, "/cd s")
	// Typing never lists (Enter submits what was typed); Tab does.
	time.Sleep(100 * time.Millisecond)
	if s.calls.count("list") != 0 {
		t.Fatal("typing listed folders")
	}
	s.Send(tab)
	tuitest.Poll(t, func() bool { return s.draft() == "/cd src/" })
	s.Send(enter)
	tuitest.Poll(t, func() bool { return len(s.relocates()) == 1 })
	if got := s.relocates()[0]; got != [3]any{"s", nil, "/work/src"} {
		t.Fatalf("relocate = %v", got)
	}
	pollContains(t, s.Fixture, "/work/src • Fixture")
	tuitest.Poll(t, s.hasNotice("Moving to /work/src…", false))
	// A missing folder: the server's error text, nothing relocated.
	typed(s.Fixture, "/cd /nope")
	s.Send(enter)
	tuitest.Poll(t, s.hasNotice("ENOENT: no such file or directory", true))
	typed(s.Fixture, "/cd tpu:/x")
	s.Send(enter)
	tuitest.Poll(t, s.hasNotice(`Unknown environment "tpu"`, true))
	if len(s.relocates()) != 1 {
		t.Fatalf("relocates = %v", s.relocates())
	}
	// Another environment: the web's note, confirmed; `~` is that machine's home.
	typed(s.Fixture, "/cd gpu:")
	s.Send(enter)
	pollContains(t, s.Fixture, "Move this session to gpu:~?")
	pollScreen(t, s.Fixture, matches(`Tools will run on gpu\. The history stays; files are not copied\.`))
	typed(s.Fixture, "y")
	tuitest.Poll(t, func() bool { return len(s.relocates()) == 2 })
	if got := s.relocates()[1]; got != [3]any{"s", "gpu", "/home/gpu"} {
		t.Fatalf("relocate = %v", got)
	}
	pollContains(t, s.Fixture, "gpu:/home/gpu • Fixture")
	// Without an argument: the prompt; Tab picks this device (starting at ~), its note is the confirmation.
	typed(s.Fixture, "/cd")
	s.Send(enter)
	pollContains(t, s.Fixture, "Working directory · on gpu (remote)")
	s.Send(tab)
	pollContains(t, s.Fixture, "Working directory · on this device")
	pollScreen(t, s.Fixture, matches(`Tools will run on this device\.`))
	s.Send(enter)
	tuitest.Poll(t, func() bool { return len(s.relocates()) == 3 })
	if got := s.relocates()[2]; got != [3]any{"s", nil, "/home/me"} {
		t.Fatalf("relocate = %v", got)
	}
	if s.calls.count("warm") == 0 {
		t.Fatal("the environments' connections start with the first use")
	}
	// /hotkeys documents the forms, the argument hints and the prompts' Tab.
	help := s.hotkeys()
	for _, pattern := range []string{`Working directory and environment`, `/cd \[\[env:\]path\]`, `/new \[\[env:\]path\]`, `next environment`} {
		if !regexp.MustCompile(pattern).MatchString(help) {
			t.Fatalf("/hotkeys lacks %s:\n%s", pattern, help)
		}
	}
}

func (s *locationSetup) hotkeys() string {
	text := ""
	s.Do(func() { text = strings.Join(tui.HotkeysText(s.Runtime.Ctx), "\n") })
	return text
}

func (s *locationSetup) run(id, args string) bool {
	ran := false
	s.Do(func() { ran = s.Runtime.Ctx.Commands().Run(id, args) })
	return ran
}

func TestNewSessionTabCyclesThisDeviceAndTheEnvironmentsNewEnvPath(t *testing.T) {
	s := newLocationSetup(t, "owner")
	s.Do(func() { s.Runtime.NavigateScreen("sessions") })
	pollContains(t, s.Fixture, "+ New session")
	s.run("app.session.new", "")
	// From a session, `/new` without an argument starts over in its env:cwd (pi's /new).
	tuitest.Poll(t, func() bool { return s.calls.count("create") == 1 })
	if got := s.calls.get("create")[0]; got != (location{env: nil, cwd: "/work"}) {
		t.Fatalf("create = %+v", got)
	}
	s.Do(func() { s.Runtime.Navigate("sessions", "", false, nil) })
	pollContains(t, s.Fixture, "+ New session")
	// The prompt: this device first, Tab moves to gpu (at its home), Enter creates there.
	s.Do(func() { s.Runtime.Ctx.Surface("sessions").(*sessionstui.Surface).Command("new") })
	pollContains(t, s.Fixture, "New session · on this device")
	pollContains(t, s.Fixture, "environment (this device, gpu)")
	s.Send(tab)
	pollContains(t, s.Fixture, "New session · on gpu (remote)")
	typed(s.Fixture, "/data")
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.calls.count("create") == 2 })
	if got := s.calls.get("create")[1]; got != (location{env: "gpu", cwd: "~/data"}) {
		t.Fatalf("create = %+v", got)
	}
	// /new with the footer's form; no prefix is this device, a relative path is below home.
	for _, each := range []struct {
		argument string
		expected location
	}{
		{"gpu:/data", location{env: "gpu", cwd: "/data"}},
		{"/tmp/x", location{env: nil, cwd: "/tmp/x"}},
		{"proj", location{env: nil, cwd: "~/proj"}},
	} {
		before := s.calls.count("create")
		s.run("app.session.new", each.argument)
		tuitest.Poll(t, func() bool { return s.calls.count("create") == before+1 })
		created := s.calls.get("create")
		if got := created[len(created)-1]; got != each.expected {
			t.Fatalf("/new %s: create = %+v", each.argument, got)
		}
	}
}

func TestGuestsCannotChangeDirectoriesAndNeverAcquireEnvs(t *testing.T) {
	s := newLocationSetup(t, "guest")
	if s.run("p2.session.cd", "/tmp") {
		t.Fatal("a guest ran /cd")
	}
	if s.run("app.session.new", "gpu:/data") {
		t.Fatal("a guest ran /new")
	}
	time.Sleep(50 * time.Millisecond)
	if s.calls.count("envsAcquired") != 0 {
		t.Fatal("a guest acquired Envs")
	}
}

func TestEnvsMovesTheSessionLocalGpuSamePathThereLocalHomeWhenThePathIsMissingCurrentMarked(t *testing.T) {
	s := newLocationSetup(t, "owner")
	s.run("p2.envs", "")
	pollContains(t, s.Fixture, "Environments (1)")
	// The session's environment is marked and selected.
	pollScreen(t, s.Fixture, matches(`→ ● This device\s+this session`))
	pollScreen(t, s.Fixture, matches(` {4}gpu {2}gpu\.example`))
	s.Send(down)
	s.Send(enter)
	// The directory prompt on gpu: /work exists there, so it stays; the note is the confirmation (as in /cd).
	pollContains(t, s.Fixture, "Working directory · on gpu (remote)")
	pollScreen(t, s.Fixture, matches(`> /work`))
	pollScreen(t, s.Fixture, matches(`Tools will run on gpu\. The history stays; files are not copied\.`))
	s.Send(enter)
	tuitest.Poll(t, func() bool { return len(s.relocates()) == 1 })
	if got := s.relocates()[0]; got != [3]any{"s", "gpu", "/work"} {
		t.Fatalf("relocate = %v", got)
	}
	pollContains(t, s.Fixture, "gpu:/work • Fixture")
	// Back to this device: gpu is marked now and selected first.
	s.run("p2.envs", "")
	pollContains(t, s.Fixture, "Environments (1)")
	pollScreen(t, s.Fixture, matches(`→ ● gpu {2}gpu\.example\s+this session`))
	s.Send(up)
	s.Send(enter)
	pollContains(t, s.Fixture, "Working directory · on this device")
	pollScreen(t, s.Fixture, matches(`Tools will run on this device\.`))
	s.Send(enter)
	tuitest.Poll(t, func() bool { return len(s.relocates()) == 2 })
	if got := s.relocates()[1]; got != [3]any{"s", nil, "/work"} {
		t.Fatalf("relocate = %v", got)
	}
	pollScreen(t, s.Fixture, matches(`(?m)^/work • Fixture`))
	// A directory gpu does not have: the prompt starts at gpu's home; Escape moves nothing.
	typed(s.Fixture, "/cd src")
	s.Send(enter)
	tuitest.Poll(t, func() bool { return len(s.relocates()) == 3 })
	s.run("p2.envs", "")
	pollContains(t, s.Fixture, "Environments (1)")
	s.Send(down)
	s.Send(enter)
	pollContains(t, s.Fixture, "Working directory · on gpu (remote)")
	pollScreen(t, s.Fixture, matches(`> /home/gpu`))
	s.Send(esc)
	time.Sleep(50 * time.Millisecond)
	if len(s.relocates()) != 3 {
		t.Fatalf("relocates = %v", s.relocates())
	}
	// Enter on the session's own environment: the prompt without the moving note (a /cd).
	s.run("p2.envs", "")
	pollContains(t, s.Fixture, "Environments (1)")
	s.Send(enter)
	pollContains(t, s.Fixture, "Working directory · on this device")
	pollScreen(t, s.Fixture, matches(`> /work/src`))
	if strings.Contains(s.Screen(), "Tools will run") {
		t.Fatal("the moving note shows on the session's own environment")
	}
	s.Send(esc)
	// /hotkeys says what Enter does there.
	if help := stripANSI(s.hotkeys()); !matches(`/envs\s+Enter moves the session`)(help) {
		t.Fatalf("/hotkeys lacks what Enter does:\n%s", help)
	}
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(text string) string { return ansi.ReplaceAllString(text, "") }

var _ sync.Mutex
