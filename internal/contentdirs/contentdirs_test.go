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
