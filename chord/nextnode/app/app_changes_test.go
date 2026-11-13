package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports the "applies changes", "applying changes one after another" and "base code" tests of
// packages/chord/test/node/app/app.test.ts (Pi main c23b9ec1). The base-code tests that start a server with no `base` given are in
// app_executable_test.go; the one with a lazy optional import has no Go form (language mechanics: an executable has no import graph). The worker
// kind's built-from file is its entry executable (a Go worker is a program, not a Node bundle). The
// serialization test holds the first build through App's `build` and `rollout` fields, where Pi replaces the component's methods.
// The "installs" test is ported below.

// untilMarks returns once the plugins' marks satisfy ok; the state's watch wakes it (no polling).
func untilMarks(t testing.TB, app *App, what string, ok func(plugins.PluginsState) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, 30*time.Second)
	defer cancel()
	watch, err := app.Plugins.Component.State().Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = watch.Stop() }()
	changed := make(chan struct{}, 1)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
		select {
		case changed <- struct{}{}:
		default:
		}
		return nil
	})
	for !ok(app.Plugins.Component.Marks()) {
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s (marks %+v)", what, app.Plugins.Component.Marks())
		}
	}
}

// untilText returns once the served Echo's `text` is want, woken by its watch.
func untilText(t testing.TB, served *plugins.NodeReference, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, 30*time.Second)
	defer cancel()
	watch, err := served.Member("text").Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = watch.Stop() }()
	changed := make(chan struct{}, 1)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
		select {
		case changed <- struct{}{}:
		default:
		}
		return nil
	})
	for {
		if got, _ := served.Member("text").Value(); got == want {
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			got, _ := served.Member("text").Value()
			t.Fatalf("Echo.text = %v, want %q", got, want)
		}
	}
}

func editEcho(t testing.TB, root, text string) {
	t.Helper()
	path := filepath.Join(root, "echo", "server.ts")
	if err := os.WriteFile(path, []byte(strings.Replace(echoServer, `"echo")`, `"`+text+`")`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAppAppliesChangesBuildsTheDirtyPluginsAndRollsThemOutToTheServer(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"echo": {"server.ts": echoServer}}, nil)
	served, err := s.app.Node.Get(background, echo, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer served.Release()
	editEcho(t, s.root, "changed")
	untilMarks(t, s.app, "the dirty mark", func(m plugins.PluginsState) bool { return len(m.Dirty) == 1 && m.Dirty[0] == "@test/echo" })
	result, err := s.app.ApplyChanges(background)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stopped != nil {
		t.Fatalf("rollout stopped: %+v", result.Stopped)
	}
	if len(result.Steps) != 1 || result.Steps[0].Name != "server" {
		t.Fatalf("steps = %+v, want the server only", result.Steps)
	}
	// Restarted: the new attempt goes live once the old one ended.
	untilText(t, served, "changed")
	if dirty := s.app.Plugins.Component.Marks().Dirty; len(dirty) != 0 {
		t.Fatalf("dirty after the rollout = %v", dirty)
	}
}

// Two ApplyChanges calls run one after another: the second builds after the first rolled out, so the newest build stays deployed.
func TestAppAppliesChangesOneAfterAnotherSoTheNewestBuildStaysDeployed(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"echo": {"server.ts": echoServer}}, nil)
	served, err := s.app.Node.Get(background, echo, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer served.Release()
	build, rollout := s.app.build, s.app.rollout
	var mu sync.Mutex
	var events []string
	builds := func() int {
		n := 0
		for _, event := range events {
			if strings.HasPrefix(event, "build") {
				n++
			}
		}
		return n
	}
	built, second, gate := make(chan struct{}), make(chan struct{}), make(chan struct{})
	// The first build read its files, then waits: unserialized, the second would build and roll out meanwhile.
	s.app.build = func(names []string, ctx context.Context) (*plugins.Built, error) {
		mu.Lock()
		n := builds() + 1
		events = append(events, fmt.Sprintf("build %d", n))
		mu.Unlock()
		result, err := build(names, ctx)
		switch n {
		case 1:
			close(built)
			<-gate
		case 2:
			close(second)
		}
		return result, err
	}
	s.app.rollout = func(steps []plugins.RolloutStep, result *plugins.Built, ctx context.Context) (plugins.RolloutResult, error) {
		mu.Lock()
		events = append(events, "rollout")
		mu.Unlock()
		return rollout(steps, result, ctx)
	}
	editEcho(t, s.root, "v2")
	untilMarks(t, s.app, "the first edit", func(m plugins.PluginsState) bool { return len(m.Dirty) == 1 })
	errs := make(chan error, 2)
	apply := func() {
		go func() {
			_, err := s.app.ApplyChanges(background)
			errs <- err
		}()
	}
	apply()
	<-built
	// Pi waits for the plugins' marks to change after this edit; here the marks do not (they hold `echo` dirty from the first edit
	// already) and build 2 reads the files when it runs, after the first rollout, so the edit is in it whenever the watcher sees it.
	editEcho(t, s.root, "v3")
	apply()
	// The second waits for the first to roll out: it does not build meanwhile (Pi waits 100 ms for it to show).
	select {
	case <-second:
		t.Fatal("a later applyChanges built while the earlier one had not rolled out")
	case <-time.After(100 * time.Millisecond):
	}
	mu.Lock()
	before := slices.Clone(events)
	mu.Unlock()
	if want := []string{"build 1"}; !slices.Equal(before, want) {
		t.Fatalf("events = %v, want %v", before, want)
	}
	close(gate)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("ApplyChanges: %v", err)
		}
	}
	mu.Lock()
	after := slices.Clone(events)
	mu.Unlock()
	if want := []string{"build 1", "rollout", "build 2", "rollout"}; !slices.Equal(after, want) {
		t.Fatalf("events = %v, want %v", after, want)
	}
	untilText(t, served, "v3")
}

// `Base` names files and directories whose change marks `base` (the app's own code changed: restart), not a rewrite with the
// same bytes (Pi's app.test.ts; its case for a worker kind's base file is below).
func TestAppMarksBaseForAChangeOfBasesFilesAndDirectories(t *testing.T) {
	for _, name := range []string{"api/v1.ts", "start.ts"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for file, text := range map[string]string{"api/v1.ts": "export {};", "start.ts": "export {};"} {
				path := filepath.Join(dir, file)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s := startTestApp(t, map[string]map[string]string{"echo": {"server.ts": echoServer}}, func(o *AppOptions) {
				o.Base = []string{filepath.Join(dir, "api"), filepath.Join(dir, "start.ts")}
			})
			if s.app.Plugins.Component.Marks().Base {
				t.Fatal("base is marked at start")
			}
			changed := filepath.Join(dir, name)
			// A rewrite with the same bytes marks nothing (Pi compares base files by hash). The barrier is a plugin edit: the
			// watcher handles events in order, so once it shows the plugin dirty it has seen the rewrite too.
			same, err := os.ReadFile(changed)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(changed, same, 0o644); err != nil {
				t.Fatal(err)
			}
			editEcho(t, s.root, "barrier")
			untilMarks(t, s.app, "the barrier edit", func(m plugins.PluginsState) bool { return len(m.Dirty) == 1 })
			if s.app.Plugins.Component.Marks().Base {
				t.Fatal("base is marked by a rewrite with the same bytes")
			}
			if err := os.WriteFile(changed, []byte("export const changed = 1;"), 0o644); err != nil {
				t.Fatal(err)
			}
			untilMarks(t, s.app, "base", func(m plugins.PluginsState) bool { return m.Base })
		})
	}
}

// Pi's `marks base for ... a file a worker kind's base was built from, not for a rewrite with the same bytes`: a worker kind's base is
// its entry executable's copy (a Go worker is a program, not a Node bundle), recorded with its bytes; the rewrite marks nothing, an edit marks `base`, and the app's
// `BaseWatched` settles once the records' first comparisons ended.
func TestAppMarksBaseForAChangeOfAWorkerKindsEntryNotForARewriteWithTheSameBytes(t *testing.T) {
	// A small program: this test starts no worker, and a rewrite of a large file is read half written by the watcher on a slow disk.
	original, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bytes := []byte("#!/bin/sh\nexit 0\n")
	entry := filepath.Join(t.TempDir(), "worker"+filepath.Ext(original))
	if err := os.WriteFile(entry, bytes, 0o755); err != nil {
		t.Fatal(err)
	}
	s := startTestApp(t, map[string]map[string]string{"echo": {"server.ts": echoServer}}, func(o *AppOptions) {
		worker := testWorker(t, project, "")
		worker.Entry = entry
		o.Workers = []AppWorkerKind{worker}
	})
	<-s.app.BaseWatched()
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked at start")
	}
	if got := s.errors.kinds(); len(got) != 0 {
		t.Fatalf("reported %v", got)
	}
	// The barrier is a plugin edit: the watcher handles events in order, so once it shows the plugin dirty it has seen the rewrite.
	if err := replaceFile(entry, bytes); err != nil {
		t.Fatal(err)
	}
	editEcho(t, s.root, "barrier")
	untilMarks(t, s.app, "the barrier edit", func(m plugins.PluginsState) bool { return len(m.Dirty) == 1 })
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked by a rewrite with the same bytes")
	}
	if err := replaceFile(entry, append(append([]byte(nil), bytes...), '\n')); err != nil {
		t.Fatal(err)
	}
	untilMarks(t, s.app, "base", func(m plugins.PluginsState) bool { return m.Base })
}

// replaceFile makes path hold data in one step (a file written beside it, renamed over it): a watcher never reads it half written.
func replaceFile(path string, data []byte) error {
	next := path + ".next"
	if err := os.WriteFile(next, data, 0o755); err != nil {
		return err
	}
	return os.Rename(next, path)
}

// gitRepository makes a local bare repository `<name>.git` of files, tagged tag, and returns its `file://` URL (no network).
func gitRepository(t *testing.T, name string, files map[string]string, tag string) string {
	t.Helper()
	git := func(cwd string, args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-c", "user.email=test@example.com", "-c", "user.name=test"}, args...)...)
		command.Dir = cwd
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	work := t.TempDir()
	git(work, "init", "--quiet")
	for file, text := range files {
		if err := os.WriteFile(filepath.Join(work, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(work, "add", "--all")
	git(work, "commit", "--quiet", "--message", tag)
	git(work, "tag", tag)
	bare := filepath.Join(t.TempDir(), name+".git")
	git(work, "clone", "--quiet", "--bare", work, bare)
	return "file://" + bare
}

func TestAppInstallsAPluginFromGitIntoTheUsersRootApplyingChangesLoadsIt(t *testing.T) {
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "npm.mjs"), []byte("process.exit(0);"), 0o644); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	url := gitRepository(t, "echo", map[string]string{"package.json": `{"name":"@bob/echo"}`, "server.ts": echoServer}, "v1")
	s := startTestApp(t, nil, func(o *AppOptions) {
		o.Install = plugins.InstallOptions{NpmCommand: []string{node, filepath.Join(tools, "npm.mjs")}}
	})
	if s.app.Plugins.Component.Marks().Set {
		t.Fatal("the set mark is set before the install")
	}
	names, err := s.app.Plugins.Install(background, plugins.PluginSource{Git: url, Ref: "v1"})
	if err != nil || !reflect.DeepEqual(names, []string{"@bob/echo"}) {
		t.Fatalf("InstallPlugin = %v, %v", names, err)
	}
	if got := dirEntries(t, filepath.Join(s.dataDir, "plugins")); !reflect.DeepEqual(got, []string{"echo"}) {
		t.Fatalf("the user's root = %v, want [echo]", got)
	}
	if !s.app.Plugins.Component.Marks().Set {
		t.Fatal("the set mark is not set after the install")
	}
	if _, err := s.app.ApplyChanges(background); err != nil {
		t.Fatal(err)
	}
	served, err := s.app.Node.Get(background, echo, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer served.Release()
	untilText(t, served, "echo")
}
