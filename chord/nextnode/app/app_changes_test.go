package app

import (
	"context"
	"os"
	"path/filepath"
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
// packages/chord/test/node/app/app.test.ts (Pi main c23b9ec1). Not ported: the three base-code tests that scan the server's own start
// script (`runStart`): the Go server is this process's binary, its `Base` files are listed by the app (nothing is scanned, so there is
// no `server_scan` report); the worker kind's built-from files (a worker's entry is an executable, not a script built by the plugins
// component); the serialization test's `component.build`/`component.rollout` hooks (a Go Plugins has no replaceable methods: the
// order is observed through the end state instead). The "installs" test waits for `installPlugin` in the Go plugins component (T9).

// untilMarks returns once the plugins' marks satisfy ok; the state's watch wakes it (no polling).
func untilMarks(t testing.TB, app *App, what string, ok func(plugins.PluginsState) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, 30*time.Second)
	defer cancel()
	watch, err := app.Plugins.State().Watch(ctx)
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
	for !ok(app.Plugins.Marks()) {
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s (marks %+v)", what, app.Plugins.Marks())
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
	if dirty := s.app.Plugins.Marks().Dirty; len(dirty) != 0 {
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
	editEcho(t, s.root, "v2")
	untilMarks(t, s.app, "the first edit", func(m plugins.PluginsState) bool { return len(m.Dirty) == 1 })
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[0] = s.app.ApplyChanges(background)
	}()
	editEcho(t, s.root, "v3")
	_, errs[1] = s.app.ApplyChanges(background)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("ApplyChanges #%d: %v", i+1, err)
		}
	}
	// A third call builds whatever the second edit left dirty if it landed after the second build began.
	if len(s.app.Plugins.Marks().Dirty) != 0 {
		if _, err := s.app.ApplyChanges(background); err != nil {
			t.Fatal(err)
		}
	}
	untilText(t, served, "v3")
}

// `Base` names files and directories whose change marks `base` (the app's own code changed: restart), not a rewrite with the
// same bytes (Pi's app.test.ts; its cases for a worker kind's base files wait for the worker base, T11).
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
			if s.app.Plugins.Marks().Base {
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
			if s.app.Plugins.Marks().Base {
				t.Fatal("base is marked by a rewrite with the same bytes")
			}
			if err := os.WriteFile(changed, []byte("export const changed = 1;"), 0o644); err != nil {
				t.Fatal(err)
			}
			untilMarks(t, s.app, "base", func(m plugins.PluginsState) bool { return m.Base })
		})
	}
}
