package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func gapDecision(id, reason string) *decision {
	return &decision{ID: id, Pkg: "a", Gap: true, Reason: reason, Candidate: "a/a.go#" + id}
}

// The baseline lists known gaps and only shrinks: an update drops the entry whose gap is closed.
func TestUpdatedBaselineDropsAClosedGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.tsv")
	write(t, path, "pkg:a/.#X\ta\tnot-exercised\tc1\npkg:a/.#Y\ta\tno-go-symbol\tc2\n")
	got, err := updatedBaseline(path, []*decision{gapDecision("pkg:a/.#X", reasonExercise), {ID: "pkg:a/.#Y"}}, "")
	if err != nil {
		t.Fatalf("closing a gap must update: %v", err)
	}
	if want := "pkg:a/.#X\ta\tnot-exercised\ta/a.go#pkg:a/.#X\n"; got != want {
		t.Fatalf("updated baseline = %q, want %q", got, want)
	}
}

// A gap the baseline does not list fails the update, naming it; the file is not touched.
func TestUpdatedBaselineRefusesANewGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.tsv")
	const before = "pkg:a/.#X\ta\tnot-exercised\tc1\n"
	write(t, path, before)
	ds := []*decision{gapDecision("pkg:a/.#X", reasonExercise), {ID: "pkg:a/.#W", Gap: true, Reason: reasonType, Detail: "boom"}}
	got, err := updatedBaseline(path, ds, "")
	if err == nil || !strings.Contains(err.Error(), "pkg:a/.#W") || !strings.Contains(err.Error(), "ALLOW_NEW") {
		t.Fatalf("a new gap must fail naming it and the explicit act, got %q, %v", got, err)
	}
	if data, _ := os.ReadFile(path); string(data) != before {
		t.Fatalf("the refused update changed the baseline: %q", data)
	}
}

// An update that closes one gap and meets a new one fails as a whole: the closed entry stays until the new gap is dealt with.
func TestUpdatedBaselineWithAClosedAndANewGapFailsAsAWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.tsv")
	write(t, path, "pkg:a/.#X\ta\tnot-exercised\tc1\npkg:a/.#Y\ta\tno-go-symbol\tc2\n")
	ds := []*decision{gapDecision("pkg:a/.#X", reasonExercise), {ID: "pkg:a/.#Y"}, gapDecision("pkg:a/.#W", reasonType)}
	if got, err := updatedBaseline(path, ds, ""); err == nil || got != "" || !strings.Contains(err.Error(), "pkg:a/.#W") {
		t.Fatalf("closed plus new must fail with no text, got %q, %v", got, err)
	}
}

// An explicit reason lists the new gap with the reason in its entry; a later plain update keeps the entry and its note.
func TestUpdatedBaselineWithAReasonListsTheNewGapAndKeepsItsNote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.tsv")
	write(t, path, "pkg:a/.#X\ta\tnot-exercised\tc1\n")
	ds := []*decision{gapDecision("pkg:a/.#W", reasonType), gapDecision("pkg:a/.#X", reasonExercise)}
	got, err := updatedBaseline(path, ds, "  Pi  adds W  ")
	if err != nil {
		t.Fatal(err)
	}
	want := "pkg:a/.#W\ta\ttype-mismatch\ta/a.go#pkg:a/.#W\tallowed: Pi adds W\npkg:a/.#X\ta\tnot-exercised\ta/a.go#pkg:a/.#X\n"
	if got != want {
		t.Fatalf("updated baseline = %q, want %q", got, want)
	}
	write(t, path, got)
	again, err := updatedBaseline(path, ds, "")
	if err != nil || again != got {
		t.Fatalf("a plain update must keep the entry and its note, got %q, %v", again, err)
	}
	if err := checkBaseline(path, ds); err != nil {
		t.Fatalf("the check must read a baseline with notes: %v", err)
	}
}

// A pin's first update has no baseline file: it writes every gap.
func TestUpdatedBaselineWithoutAFileWritesTheWholeList(t *testing.T) {
	got, err := updatedBaseline(filepath.Join(t.TempDir(), "absent.tsv"), []*decision{gapDecision("pkg:a/.#X", reasonExercise), {ID: "pkg:a/.#Y"}}, "")
	if err != nil || got != "pkg:a/.#X\ta\tnot-exercised\ta/a.go#pkg:a/.#X\n" {
		t.Fatalf("first baseline = %q, %v", got, err)
	}
}

// The command, as make interface-gaps and make interface-gaps-update run it: a gap the tree gained is red in the check, the update
// that follows refuses it and writes nothing, so the check stays red; only the update with a reason lists it.
func TestUpdateOverANewGapFailsWritesNothingAndTheCheckStaysRed(t *testing.T) {
	root, reach := mirrorFixture(t)
	writeMirror(t, root, pigversion.UpstreamVersion, pigversion.UpstreamRef, pigversion.UpstreamCommit, pigversion.UpstreamPackageVersion)
	args := []string{"-root", root, "-reach", reach, "-out", filepath.Join(root, "build/interface-gaps")}
	run := func(extra ...string) (int, string) {
		return runAutobind(t, append(append([]string{}, args...), extra...)...)
	}
	if code, out := run("-update"); code != 0 {
		t.Fatalf("first update exited %d:\n%s", code, out)
	}
	if code, out := run("-check"); code != 0 {
		t.Fatalf("check of the fresh ledger exited %d:\n%s", code, out)
	}
	// The tree has gaps (the fixture's own); a baseline that lacks one of them is the state of a tree that gained it.
	baselinePath := filepath.Join(root, "test/parity/interface-closure/gaps/interface-gaps-v"+pigversion.UpstreamVersion+".tsv")
	listed, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	const gained = "pkg:fx/.#Widget::property:name"
	var kept []string
	for line := range strings.SplitSeq(strings.TrimSuffix(string(listed), "\n"), "\n") {
		if !strings.HasPrefix(line, gained+"\t") {
			kept = append(kept, line)
		}
	}
	if len(kept) != strings.Count(string(listed), "\n")-1 {
		t.Fatalf("the fixture's baseline does not list %s:\n%s", gained, listed)
	}
	write(t, baselinePath, strings.Join(kept, "\n")+"\n")
	if code, _ := run("-check"); code == 0 {
		t.Fatal("the check passed over a tree with a new gap")
	}
	before := treeSnapshot(t, root)
	code, out := run("-update")
	if code == 0 || !strings.Contains(out, gained) {
		t.Fatalf("the update over a new gap exited %d, want a failure naming %s:\n%s", code, gained, out)
	}
	after := treeSnapshot(t, root)
	for path, content := range after {
		if was, ok := before[path]; !ok || was != content {
			t.Errorf("the refused update wrote %s", path)
		}
	}
	if len(after) != len(before) {
		t.Errorf("the refused update removed a file")
	}
	if code, _ := run("-check"); code == 0 {
		t.Fatal("the check is green after a refused update")
	}
	if code, out := run("-update", "-allow-new", "Pi adds Widget.name"); code != 0 {
		t.Fatalf("the update with a reason exited %d:\n%s", code, out)
	}
	baseline, err := os.ReadFile(baselinePath)
	if err != nil || !strings.Contains(string(baseline), gained+"\t") || !strings.Contains(string(baseline), "\tallowed: Pi adds Widget.name\n") {
		t.Fatalf("baseline after the update with a reason = %q, %v", baseline, err)
	}
	if code, out := run("-check"); code != 0 {
		t.Fatalf("the check after the update with a reason exited %d:\n%s", code, out)
	}
	if code, out := run("-update"); code != 0 {
		t.Fatalf("a plain update over the listed gap exited %d:\n%s", code, out)
	}
	if again, _ := os.ReadFile(baselinePath); !bytes.Equal(again, baseline) {
		t.Fatalf("a plain update changed the listed baseline:\n%s\n--\n%s", baseline, again)
	}
	if code, out := run("-allow-new", "x"); code == 0 || !strings.Contains(out, "-allow-new needs -update") {
		t.Fatalf("-allow-new without -update exited %d:\n%s", code, out)
	}
}
