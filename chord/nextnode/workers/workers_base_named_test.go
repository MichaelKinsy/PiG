package workers

import (
	"os"
	"path/filepath"
	"testing"
)

// The named base directory of the kit's workers (`cache/workers/<kind>/`) as the app passes it: the copy is staged beside it and
// renamed in, so how the path is written (a trailing separator, a stale staged directory of a process that ended mid-build) must not
// change where the base is or leave anything beside it.

// A BaseDir ending in a separator names the same directory: the build neither stages inside it (the commit empties it) nor fails.
func TestTheWorkerBaseANamedDirectoryEndingInASeparatorBuildsTheSameBase(t *testing.T) {
	named := filepath.Join(t.TempDir(), "bases", "worker")
	f := newFleet(t, fleetOptions{baseDir: named + string(filepath.Separator)})
	if built := f.workers.Build(); built.Err != nil {
		t.Fatalf("the build failed: %v", built.Err)
	}
	copied := filepath.Join(named, "worker"+filepath.Ext(fixtureEntry(t)))
	if names := listDir(t, named); len(names) != 1 || filepath.Join(named, names[0]) != copied {
		t.Fatalf("the directory = %v, want only %s", names, copied)
	}
	if names := listDir(t, filepath.Dir(named)); len(names) != 1 || names[0] != "worker" {
		t.Fatalf("beside the base directory = %v, want only it", names)
	}
}

// A staged directory an earlier start left beside the named one (its build outlived its close and the process ended) goes with the
// next build: the app holds the data directory's lock then, so nothing else writes there.
func TestTheWorkerBaseABuildRemovesTheStagedDirectoriesAnEarlierProcessLeftBesideTheNamedOne(t *testing.T) {
	named := filepath.Join(t.TempDir(), "bases", "worker")
	stale := filepath.Join(filepath.Dir(named), ".worker-build-x")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "worker"), []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(filepath.Dir(named), ".other-build-x")
	if err := os.MkdirAll(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	f := newFleet(t, fleetOptions{baseDir: named})
	if built := f.workers.Build(); built.Err != nil {
		t.Fatalf("the build failed: %v", built.Err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("the stale staged directory after the build: %v, want it removed", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("another kind's directory: %v, want it left", err)
	}
}
