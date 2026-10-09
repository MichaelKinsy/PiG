package piglet

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func item(piglet string, ageHours int, size int64) pruneItem {
	return pruneItem{piglet: piglet, when: time.Unix(1_000_000, 0).Add(-time.Duration(ageHours) * time.Hour), size: size, record: managedRecord{RecordInfo: RecordInfo{Path: fmt.Sprintf("%s-%dh", piglet, ageHours)}}}
}

func names(items []pruneItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.record.Path
	}
	return out
}

// The newest keep of each Piglet stay whatever the limit says, and without a
// limit everything older goes.
func TestSelectPrunableKeepsTheNewestOfEachPiglet(t *testing.T) {
	items := []pruneItem{item("a", 1, 10), item("a", 2, 10), item("a", 3, 10), item("a", 4, 10), item("b", 1, 10), item("b", 9, 10)}
	got := names(selectPrunable(items, 2, nil, 60))
	if len(got) != 2 || got[0] != "a-4h" || got[1] != "a-3h" {
		t.Fatalf("selected %v, want the two oldest of a, oldest first", got)
	}
	if got := selectPrunable(items, 0, nil, 60); len(got) != 6 {
		t.Fatalf("--keep 0 selected %d, want all 6", len(got))
	}
	if got := selectPrunable(items, 10, nil, 60); len(got) != 0 {
		t.Fatalf("--keep 10 selected %v", names(got))
	}
}

// A build for several targets records one Binary per target. Each target keeps
// its own newest builds, so an older target of the same build is not removed
// because newer Binaries of other targets exist.
func TestSelectPrunableKeepsTheNewestOfEachTarget(t *testing.T) {
	targeted := func(target string, ageHours int) pruneItem {
		it := item("p", ageHours, 10)
		it.record.Target = target
		it.record.Path = fmt.Sprintf("%s-%dh", target, ageHours)
		return it
	}
	items := []pruneItem{
		targeted("linux/amd64", 1), targeted("linux/arm64", 2), targeted("darwin/arm64", 3),
		targeted("linux/amd64", 10), targeted("linux/arm64", 11), targeted("darwin/arm64", 12),
	}
	if got := selectPrunable(items, 2, nil, 60); len(got) != 0 {
		t.Fatalf("--keep 2 selected %v; every target has only two builds", names(got))
	}
	got := names(selectPrunable(items, 1, nil, 60))
	if len(got) != 3 || got[0] != "darwin/arm64-12h" || got[1] != "linux/arm64-11h" || got[2] != "linux/amd64-10h" {
		t.Fatalf("--keep 1 selected %v, want the older build of each target, oldest first", got)
	}
}

// With a limit, candidates go oldest first and only until the store fits;
// what --keep protects is never counted as removable, so the limit can be missed.
func TestSelectPrunableStopsAtTheLimitOldestFirst(t *testing.T) {
	items := []pruneItem{item("a", 1, 100), item("a", 2, 100), item("a", 3, 100), item("b", 5, 100), item("b", 6, 100)}
	limit := int64(350)
	got := names(selectPrunable(items, 1, &limit, 500))
	if len(got) != 2 || got[0] != "b-6h" || got[1] != "a-3h" {
		t.Fatalf("selected %v, want the oldest removable builds across Piglets", got)
	}
	tight := int64(0)
	if got := selectPrunable(items, 1, &tight, 500); len(got) != 3 {
		t.Fatalf("limit 0 selected %v; the newest of each Piglet must stay", names(got))
	}
}

func TestPruneItemsNeverOffersPulledInstalls(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	built := managedRecord{Piglet: "p", RecordInfo: RecordInfo{Path: write("built.json"), ArtifactPath: write("built.bin"), ResolutionPath: "res-built"}}
	pulled := managedRecord{Piglet: "p", RecordInfo: RecordInfo{Path: write("receipt.json"), ArtifactPath: write("pulled.bin"), CurrentPath: write("current"), ResolutionPath: "res-pulled"}}
	missing := managedRecord{Piglet: "p", RecordInfo: RecordInfo{Path: write("m.json"), ArtifactPath: filepath.Join(dir, "gone.bin"), ResolutionPath: "res-missing"}}
	items, staying := pruneItems([]managedRecord{built, pulled, missing})
	if len(items) != 1 || items[0].record.Path != built.Path {
		t.Fatalf("items = %v", names(items))
	}
	if !staying["res-pulled"] || !staying["res-missing"] || staying["res-built"] {
		t.Fatalf("staying = %v", staying)
	}
}

// End to end against a store written by the real record fixtures: the oldest
// build goes with its artifact and records, --dry-run changes nothing, and a
// resolution record shared with a Binary that stays is kept.
func TestPigletPruneRemovesOldBuildsOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	var binaries []string
	for i, digest := range []string{strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64)} {
		path := writeRecordFixtureWithPigletDigest(t, root, "tool", digest)
		when := time.Now().Add(-time.Duration(3-i) * 24 * time.Hour)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
		binaries = append(binaries, path)
	}
	artifactRoot := filepath.Join(root, "artifacts", "piglets", "tool")

	var out, errOut bytes.Buffer
	if code := cmdPrune([]string{"--keep", "2", "--dry-run"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "1 would be removed") {
		t.Fatalf("dry run code=%d out=%s err=%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(binaries[0]); err != nil {
		t.Fatalf("dry run removed the record: %v", err)
	}

	out.Reset()
	errOut.Reset()
	if code := cmdPrune([]string{"--keep", "2"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "1 removed") {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(binaries[0]); !os.IsNotExist(err) {
		t.Fatalf("oldest record kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactRoot, strings.Repeat("1", 64))); !os.IsNotExist(err) {
		t.Fatalf("oldest artifact directory kept: %v", err)
	}
	for _, kept := range binaries[1:] {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("newer record removed: %v", err)
		}
	}
	// The listing validates every record against its resolution and artifact. The kept builds are
	// two records of the one binary-only Piglet.
	piglets, err := List()
	if err != nil || len(piglets) != 1 || len(piglets[0].Records) != 2 {
		t.Fatalf("store is inconsistent after prune: %v (%#v, want one Piglet with the 2 kept builds)", err, piglets)
	}
}

func TestPigletPruneRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{{"--keep"}, {"--keep", "-1"}, {"--max-size", "lots"}, {"extra"}} {
		var out, errOut bytes.Buffer
		if code := cmdPrune(args, &out, &errOut); code != 2 {
			t.Errorf("%v: code %d, want 2 (%s)", args, code, errOut.String())
		}
	}
}
