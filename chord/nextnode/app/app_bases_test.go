package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	nodesurfaces "github.com/MichaelKinsy/PiG/chord/nextnode/surfaces"
)

// Ports the "bases built after the listen" tests of packages/chord/test/node/app/app.test.ts (Pi 284e63d9): the app answers before
// its bases are built, a page request waits for its base, an aborted Apply waiting at the web step for the initial build stops
// there, and a base that fails after the listen is reported and the server runs on (the page answers 503).
//
// Forced differences. A base build that waits is a web entry importing a named pipe (Linux, as Pi's mkfifo). A Go worker kind's
// base is a copy of its executable (D600): its build cannot be held, so Pi's tests of a worker kind's base waiting for the build
// (`answers before a worker kind's base is built`) and of a closed start's late worker-base build (the same component-level tests
// in chord/nextnode/workers: a component that closed copies nothing) have no app-level counterpart; the worker kind's part of the
// failing-base test below is a start that fails, as Pi's. Pi's tests of the scan of the server's own code held on a pipe (`answers
// before the scan ... finished`, `closes without waiting for a scan ... stalled on a read`) are the two tests of a stalled web
// base: the scan runs after the bases are built, and the close does not wait for a build. Its `settles baseWatched only once the
// base bundles' records were compared` is `TestRecordBaseSettlesOnceItsFirstComparisonsEnded` (the plugins' watcher) and
// `TestAppMarksBaseForAChangeOfAWorkerKindsEntry…` (app_changes_test.go). A link's upgrade waiting for the bases is D604.

// plainPage is a page showing the base script's URL.
func plainPage(_ chordhttp.HttpRequest, assets nodesurfaces.PageAssets) (string, error) {
	return "<p>" + assets.Script + "</p>", nil
}

func withPublicWeb(entry string, page func(chordhttp.HttpRequest, nodesurfaces.PageAssets) (string, error)) func(*AppOptions) {
	return func(o *AppOptions) {
		o.Kinds = append(o.Kinds, AppKind{Name: "web", Platform: plugins.PlatformBrowser})
		o.Web = &AppWeb{Entry: entry, Page: page, Public: true}
	}
}

func TestAppReportsABaseThatFailsAfterTheListenAndRunsOnThePageAnswers503TheKindsWorkersFailTheirStart(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(main, []byte("export const broken = ;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, func(o *AppOptions) {
		withPublicWeb(main, func(chordhttp.HttpRequest, nodesurfaces.PageAssets) (string, error) { return "<p>page</p>", nil })(o)
		worker := testWorker(t, project, "")
		worker.Entry = filepath.Join(dir, "no-such-worker")
		o.Workers = []AppWorkerKind{worker}
	})
	s.errors.expect()
	s.errors.mu.Lock()
	reported := slices.Clone(s.errors.list)
	s.errors.mu.Unlock()
	var names []string
	for _, each := range reported {
		if each.Kind != AppErrorBaseBuild {
			t.Fatalf("reported %+v, want only base_build", each)
		}
		names = append(names, each.Base)
		failure, ok := each.Error.(error)
		switch each.Base {
		case "web":
			if !ok || !strings.Contains(failure.Error(), `Unexpected ";"`) {
				t.Fatalf("web: %v, want esbuild's error", each.Error)
			}
		case "project":
			if !ok || !strings.Contains(failure.Error(), "the worker entry cannot be started") {
				t.Fatalf("project: %v, want the entry that cannot be started", each.Error)
			}
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"project", "web"}) {
		t.Fatalf("base_build reports for %v, want project and web", names)
	}
	if got := s.app.Web.Base(); got != "" {
		t.Fatalf("base = %q, want none", got)
	}
	for _, path := range []string{"/", "/_chord/web/base"} {
		if got := surfaceGet(t, s.port, path, nil).status; got != 503 {
			t.Fatalf("GET %s = %d, want 503", path, got)
		}
	}
	if got := surfaceGet(t, s.port, "/_chord/owner", nil).status; got != 200 {
		t.Fatalf("owner page = %d, want 200: the server runs on", got)
	}
	held, err := s.app.Host.Hold(background, notes, delta.JsonObjectOf("id", "p1"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	deadline := time.Now().Add(30 * time.Second)
	for held.State().Value().Kind != host.RefStateFailed {
		if time.Now().After(deadline) {
			t.Fatalf("state = %s, want failed", held.State().Value().Kind)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cause := held.State().Value().Cause; cause.Kind != data.FailureWaiting || cause.Error.Message != "worker failed to start" {
		t.Fatalf("cause = %+v / %+v", cause, cause.Error)
	}
}
