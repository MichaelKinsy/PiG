package contentdirs

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// makeDir creates parent/name with a modification time age ago.
func makeDir(t *testing.T, parent, name string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Eviction keeps the Keep most recently used directories of one base, counting the caller's own even when it is the least recently used, and
// never touches another base's directories or names that are not <base>-<16 hex digits>, such as an older PiG's unhashed directory.
func TestEvictKeepsTheMostRecentlyUsedAndTheCallersOwn(t *testing.T) {
	parent := t.TempDir()
	own := makeDir(t, parent, "sdk-0000000000000000", 50*time.Hour)
	var others []string
	for i, age := range []time.Duration{1, 2, 3, 4, 5} {
		others = append(others, makeDir(t, parent, "sdk-"+string(rune('a'+i))+"000000000000000", age*time.Hour))
	}
	legacy := makeDir(t, parent, "sdk", 100*time.Hour)
	sibling := makeDir(t, parent, "sdk-py-1111111111111111", 100*time.Hour)
	odd := makeDir(t, parent, "sdk-notahexhash00", 100*time.Hour)

	removed, err := Evict(parent, "sdk", own)
	if err != nil {
		t.Fatal(err)
	}
	if want := others[Keep-1:]; !slices.Equal(removed, want) {
		t.Fatalf("removed %v, want %v", removed, want)
	}
	for _, kept := range append([]string{own, legacy, sibling, odd}, others[:Keep-1]...) {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s was removed: %v", kept, err)
		}
	}
}

// Touch writes a use time only when the recorded one is an hour old or more, so a warm start usually writes nothing.
func TestTouchRecordsUseAtMostHourly(t *testing.T) {
	parent := t.TempDir()
	stale := makeDir(t, parent, "docs-0000000000000000", 2*time.Hour)
	fresh := makeDir(t, parent, "docs-1111111111111111", 10*time.Minute)
	freshInfo, _ := os.Stat(fresh)
	Touch(stale)
	Touch(fresh)
	if info, _ := os.Stat(stale); time.Since(info.ModTime()) > time.Minute {
		t.Fatalf("stale dir use time %v was not refreshed", info.ModTime())
	}
	if info, _ := os.Stat(fresh); !info.ModTime().Equal(freshInfo.ModTime()) {
		t.Fatalf("fresh dir use time changed from %v to %v", freshInfo.ModTime(), info.ModTime())
	}
}

func TestNameRequiresAHexHash(t *testing.T) {
	if got, err := Name("sdk", "0123456789abcdef0123"); err != nil || got != "sdk-0123456789abcdef" {
		t.Fatalf("Name = %q, %v", got, err)
	}
	if _, err := Name("sdk", "short"); err == nil {
		t.Fatal("Name accepted a short hash")
	}
}

// A process that holds a directory keeps it, whatever its use time: another build's eviction runs in another process and sees only the lease. The held directory
// does not count among the kept ones, so the idle directories beyond Keep are still evicted around it.
func TestEvictLeavesADirectoryAProcessHolds(t *testing.T) {
	parent := t.TempDir()
	own := makeDir(t, parent, "docs-0000000000000000", time.Hour)
	held := makeDir(t, parent, "docs-1111111111111111", 90*time.Hour)
	release := Hold(held)
	t.Cleanup(release)
	var idle []string
	for i := range Keep + 1 {
		idle = append(idle, makeDir(t, parent, "docs-"+string(rune('a'+i))+"000000000000000", time.Duration(i+2)*time.Hour))
	}
	removed, err := Evict(parent, "docs", own)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(held); err != nil {
		t.Fatalf("a held directory was evicted: %v", err)
	}
	if want := idle[Keep-1:]; !slices.Equal(removed, want) {
		t.Fatalf("removed %v, want the idle directories beyond Keep: %v", removed, want)
	}

	// Once the holder lets go, the directory is an ordinary old one and goes with its lease file.
	release()
	removed, err = Evict(parent, "docs", own)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(removed, held) {
		t.Fatalf("removed %v, want the released directory %s", removed, held)
	}
	if _, err := os.Stat(held + leaseSuffix); !os.IsNotExist(err) {
		t.Fatalf("the lease file of an evicted directory stayed: %v", err)
	}
}

// A directory with no lease file has no holder (an older PiG never holds one), and a lease file nobody holds, such as a dead holder's, does not keep its directory.
func TestEvictRemovesADirectoryWithNoLiveHolder(t *testing.T) {
	parent := t.TempDir()
	own := makeDir(t, parent, "docs-0000000000000000", time.Hour)
	for i := range Keep - 1 {
		makeDir(t, parent, "docs-"+string(rune('a'+i))+"000000000000000", time.Duration(i+2)*time.Hour)
	}
	bare := makeDir(t, parent, "docs-1111111111111111", 80*time.Hour)
	stale := makeDir(t, parent, "docs-2222222222222222", 90*time.Hour)
	if err := os.WriteFile(stale+leaseSuffix, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := Evict(parent, "docs", own)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(removed, bare) || !slices.Contains(removed, stale) {
		t.Fatalf("removed %v, want %s and %s", removed, bare, stale)
	}
	if _, err := os.Stat(stale + leaseSuffix); !os.IsNotExist(err) {
		t.Fatalf("a dead holder's lease file stayed: %v", err)
	}
	if _, err := os.Stat(bare + leaseSuffix); !os.IsNotExist(err) {
		t.Fatalf("evicting a directory with no lease created one: %v", err)
	}
}

// Holding a directory twice holds it once, and a release does not drop the other holder's lease.
func TestHoldTwiceHoldsOnce(t *testing.T) {
	parent := t.TempDir()
	dir := makeDir(t, parent, "docs-1111111111111111", 90*time.Hour)
	first := Hold(dir)
	t.Cleanup(first)
	second := Hold(dir)
	second()
	own := makeDir(t, parent, "docs-0000000000000000", time.Hour)
	for i := range Keep - 1 {
		makeDir(t, parent, "docs-"+string(rune('a'+i))+"000000000000000", time.Duration(i+2)*time.Hour)
	}
	if _, err := Evict(parent, "docs", own); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the second Hold's release dropped the first's lease: %v", err)
	}
}
