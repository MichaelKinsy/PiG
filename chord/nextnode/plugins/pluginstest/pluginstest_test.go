package pluginstest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeWatcher is a watcher that records what the barrier asks and answers as the test says.
type fakeWatcher struct {
	watching bool
	// reached answers the call-th Watches (from 1).
	reached func(call int) bool
	// comparing, when non-nil, answers the call-th ComparingBase (from 1); nil means false.
	comparing      func(call int) bool
	comparingCalls int
	trees          []string
	asked          []string
	// made is whether each directory asked about was there when it was asked.
	made []bool
}

func (w *fakeWatcher) WatchTree(dir string) ([]string, bool) {
	w.trees = append(w.trees, dir)
	return []string{dir}, w.watching
}

func (w *fakeWatcher) Watches(dir string) bool {
	info, err := os.Stat(dir)
	w.asked = append(w.asked, dir)
	w.made = append(w.made, err == nil && info.IsDir())
	return w.reached(len(w.asked))
}

func (w *fakeWatcher) ComparingBase() bool {
	w.comparingCalls++
	if w.comparing == nil {
		return false
	}
	return w.comparing(w.comparingCalls)
}

// failing is a test whose Fatalf is recorded and ends the goroutine it is called on, as a test's does.
type failing struct {
	testing.TB
	fatal string
}

func (f *failing) Helper() {}

func (f *failing) Fatalf(format string, args ...any) {
	f.fatal = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func TestSettleWaitsForNothingWhenTheWatcherDoesNotWatch(t *testing.T) {
	watcher := &fakeWatcher{reached: func(int) bool { return false }}
	Settle(t, watcher)
	if len(watcher.trees) != 1 || len(watcher.asked) != 0 {
		t.Fatalf("trees = %v, asked = %v, want one tree and no question", watcher.trees, watcher.asked)
	}
	entries, err := os.ReadDir(watcher.trees[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%d entries made in the barrier's directory of a watcher that does not watch, want none", len(entries))
	}
}

func TestSettleReturnsOnceTheWatcherWatchesTheDirectoryItMadeInTheTreeItHadItWatch(t *testing.T) {
	watcher := &fakeWatcher{watching: true, reached: func(call int) bool { return call == 3 }}
	Settle(t, watcher)
	if len(watcher.trees) != 1 || len(watcher.asked) != 3 {
		t.Fatalf("trees = %v, asked = %v, want one tree and three questions (the third answered yes)", watcher.trees, watcher.asked)
	}
	for i, dir := range watcher.asked {
		if dir != watcher.asked[0] || filepath.Dir(dir) != watcher.trees[0] || !watcher.made[i] {
			t.Fatalf("question %d is about %s (there: %v), want one directory made in %s before the first question", i+1, dir, watcher.made[i], watcher.trees[0])
		}
	}
}

func TestSettleMakesANewDirectoryEachTimeAndHasTheSameTreeWatchedAgain(t *testing.T) {
	watcher := &fakeWatcher{watching: true, reached: func(int) bool { return true }}
	Settle(t, watcher)
	Settle(t, watcher)
	if len(watcher.trees) != 2 || watcher.trees[0] != watcher.trees[1] {
		t.Fatalf("trees = %v, want the same tree twice (a discovery may have dropped it)", watcher.trees)
	}
	if len(watcher.asked) != 2 || watcher.asked[0] == watcher.asked[1] || !watcher.made[0] || !watcher.made[1] {
		t.Fatalf("asked = %v (there: %v), want two different directories, each made", watcher.asked, watcher.made)
	}
}

func TestSettleFailsTheTestWhenTheWatcherHasNotReachedTheDirectoryAtTheBound(t *testing.T) {
	watcher := &fakeWatcher{watching: true, reached: func(int) bool { return false }}
	test := &failing{TB: t}
	ended := make(chan struct{})
	began := time.Now()
	go func() {
		defer close(ended)
		settle(test, watcher, 40*time.Millisecond)
	}()
	<-ended
	if want := "timed out after 40ms waiting for the watcher to handle a directory made after the edits"; test.fatal != want {
		t.Fatalf("failure = %q, want %q", test.fatal, want)
	}
	if waited := time.Since(began); waited < 40*time.Millisecond {
		t.Fatalf("failed after %v, want the bound (40ms) waited out", waited)
	}
	if len(watcher.asked) == 0 {
		t.Fatal("the watcher was never asked")
	}
}

func TestSettleWaitsUntilComparingBaseIsFalseAfterTheDirectoryIsWatched(t *testing.T) {
	watcher := &fakeWatcher{
		watching:  true,
		reached:   func(call int) bool { return call == 2 },
		comparing: func(call int) bool { return call < 3 },
	}
	Settle(t, watcher)
	if len(watcher.asked) != 2 || watcher.comparingCalls != 3 {
		t.Fatalf("asked = %d, comparingCalls = %d, want 2 and 3", len(watcher.asked), watcher.comparingCalls)
	}
}

func TestSettleFailsTheTestWhenComparingBaseStaysTrueAtTheBound(t *testing.T) {
	watcher := &fakeWatcher{
		watching:  true,
		reached:   func(int) bool { return true },
		comparing: func(int) bool { return true },
	}
	test := &failing{TB: t}
	ended := make(chan struct{})
	began := time.Now()
	go func() {
		defer close(ended)
		settle(test, watcher, 40*time.Millisecond)
	}()
	<-ended
	if want := "timed out after 40ms waiting for the watcher to finish base file comparisons"; test.fatal != want {
		t.Fatalf("failure = %q, want %q", test.fatal, want)
	}
	if waited := time.Since(began); waited < 40*time.Millisecond {
		t.Fatalf("failed after %v, want the bound (40ms) waited out", waited)
	}
	if watcher.comparingCalls == 0 {
		t.Fatal("ComparingBase was never asked")
	}
}

func TestABarrierEndsWithTheTestThatMadeItAndTheNextSettleMakesItsOwn(t *testing.T) {
	watcher := &fakeWatcher{watching: true, reached: func(int) bool { return true }}
	t.Run("first", func(t *testing.T) { Settle(t, watcher) })
	if _, held := barriers.Load(watcher); held {
		t.Fatal("the barrier is still held after the test that made it ended")
	}
	if _, err := os.Stat(watcher.trees[0]); !os.IsNotExist(err) {
		t.Fatalf("the first barrier's directory: %v, want it gone with its test", err)
	}
	Settle(t, watcher)
	if len(watcher.trees) != 2 || watcher.trees[0] == watcher.trees[1] || !watcher.made[1] {
		t.Fatalf("trees = %v (second directory there: %v), want a new tree with its directory made", watcher.trees, watcher.made)
	}
}

// helperPackage is this package's import path.
const helperPackage = "github.com/MichaelKinsy/PiG/chord/nextnode/plugins/pluginstest"

func TestNoProductionCodeImportsTheTestHelper(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the module's root is not four directories up: %v", err)
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == "node_modules" || name == "tmp" || (strings.HasPrefix(name, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), `"`+helperPackage+`"`) {
			t.Errorf("%s imports %s", path, helperPackage)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
