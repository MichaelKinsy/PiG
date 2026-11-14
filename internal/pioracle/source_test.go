package pioracle

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const otherCommit = "5e24c6b1f9b5e3049ec07b2101a105b11d1d1b4f"

// fakeOverrideTree lays out what `pin.py override-tree` builds for otherCommit: the finished mark, the packages of the source build, the
// packs' manifest and an install whose pi-durable npm recorded with `integrity` (the pack's is sha512-other).
func fakeOverrideTree(t *testing.T, integrity string) string {
	t.Helper()
	tree := t.TempDir()
	files := map[string]string{
		".ready":                                          otherCommit + "\n",
		"packages/durable/src/signals.ts":                 "",
		"third_party/pi-packs/manifest.json":              `{"commit":"` + otherCommit + `","packs":{"earendil-works-pi-durable-1.5.2.tgz":{"name":"@earendil-works/pi-durable","integrity":"sha512-other"}}}`,
		"durable/interop/node_modules/.package-lock.json": `{"packages":{"node_modules/@earendil-works/pi-durable":{"version":"1.5.2","resolved":"file:../../third_party/pi-packs/earendil-works-pi-durable-1.5.2.tgz","integrity":"` + integrity + `"}}}`,
	}
	for rel, text := range files {
		path := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return tree
}

// A run at the pin reads the repository's own Pi: the mirror and durable/interop, where the vendored packs are installed.
func TestARunAtThePinReadsTheRepositorysPi(t *testing.T) {
	t.Setenv(OverrideTreeEnv, "")
	root := repoRoot(t)
	if got, want := Packages(t), filepath.Join(root, ".upstream", "current", "packages"); got != want {
		t.Fatalf("Packages = %s, want %s", got, want)
	}
	if got, want := Project(t), filepath.Join(root, "durable", "interop")+string(filepath.Separator); got != want {
		t.Fatalf("Project = %s, want %s", got, want)
	}
}

// The carried oracle run names another Pi's tree, and every path the tests read is inside it: the link form it replaces pointed
// durable/interop/node_modules and .upstream/current at the other Pi, and a run that was killed left them there for the next gate.
func TestARunThatNamesAnotherPiReadsOnlyItsTree(t *testing.T) {
	tree := fakeOverrideTree(t, "sha512-other")
	t.Setenv(OverrideTreeEnv, tree)
	if got, want := Packages(t), filepath.Join(tree, "packages"); got != want {
		t.Fatalf("Packages = %s, want %s", got, want)
	}
	if got, want := Project(t), filepath.Join(tree, "durable", "interop")+string(filepath.Separator); got != want {
		t.Fatalf("Project = %s, want %s", got, want)
	}
}

func TestAnOverrideTreeMustBeFinishedAndHoldItsOwnPacks(t *testing.T) {
	if err := checkOverrideTree(fakeOverrideTree(t, "sha512-other")); err != nil {
		t.Fatalf("a finished tree: %v", err)
	}
	unfinished := fakeOverrideTree(t, "sha512-other")
	if err := os.Remove(filepath.Join(unfinished, ".ready")); err != nil {
		t.Fatal(err)
	}
	if err := checkOverrideTree(unfinished); err == nil || !strings.Contains(err.Error(), "finished") {
		t.Fatalf("a tree without its finished mark = %v, want an error", err)
	}
	if err := checkOverrideTree(fakeOverrideTree(t, "sha512-pinned")); err == nil || !strings.Contains(err.Error(), "not the vendored") {
		t.Fatalf("an install of other packs = %v, want an error", err)
	}
}

// outcome records how a helper ended the test it was given.
type outcome struct {
	testing.TB
	skipped, failed string
}

func (o *outcome) Helper()           {}
func (o *outcome) Skip(args ...any)  { o.skipped = fmt.Sprint(args...); runtime.Goexit() }
func (o *outcome) Fatal(args ...any) { o.failed = fmt.Sprint(args...); runtime.Goexit() }
func (o *outcome) Fatalf(format string, args ...any) {
	o.failed = fmt.Sprintf(format, args...)
	runtime.Goexit()
}
func (o *outcome) Skipf(format string, args ...any) {
	o.skipped = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func endOf(run func(testing.TB)) *outcome {
	o := &outcome{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(o)
	}()
	<-done
	return o
}

// A test of carried work skips at the pin. In the carried run a skip would turn a Pi without the work into a green run, so it fails.
func TestWorkThePiLacksSkipsAtThePinAndFailsInACarriedRun(t *testing.T) {
	t.Setenv(OverrideTreeEnv, "")
	if o := endOf(func(tb testing.TB) { SkipAtPin(tb, "no suspendable work") }); o.skipped != "no suspendable work" || o.failed != "" {
		t.Fatalf("at the pin: skipped %q, failed %q", o.skipped, o.failed)
	}
	t.Setenv(OverrideTreeEnv, fakeOverrideTree(t, "sha512-other"))
	if o := endOf(func(tb testing.TB) { SkipAtPin(tb, "no suspendable work") }); o.skipped != "" || !strings.Contains(o.failed, "lacks the work") {
		t.Fatalf("in a carried run: skipped %q, failed %q", o.skipped, o.failed)
	}
}
