package pigsdk

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pig additive (D19): "Only after a stage actually happens, under the SDK stage lock, Pig evicts other builds' directories
// beyond the 4 most recently used per language (contentdirs.Keep, its own always kept) ... builds of the unhashed
// sdk[-py|-rs] directories an older PiG stages into, which Pig never writes or removes."
//
// Six other builds' directories of different ages stand beside this build's own, in every language this build stages, with
// a legacy unhashed directory far older than all of them. A warm start evicts nothing. A stage keeps this build's own and the
// three most recently used others of each language, removes the other three, counts each language apart, and leaves the
// legacy directory.
func TestStagingKeepsTheOwnAndThreeMostRecentlyUsedOthersPerLanguage(t *testing.T) {
	root := t.TempDir()
	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "state", "pigsdk")
	type language struct {
		own, legacy string
		others      []string
	}
	var languages []language
	for n, b := range bundles() {
		own, err := b.stagedDir(root)
		if err != nil {
			t.Fatal(err)
		}
		base := sdkBase(b.lang)
		l := language{own: own, legacy: filepath.Join(parent, base)}
		// Ages 1 h to 6 h, written in no particular order so that the ranking is by use time, not by name or creation.
		for _, hours := range []int{4, 1, 6, 3, 5, 2} {
			dir := filepath.Join(parent, fmt.Sprintf("%s-%016x", base, n*0x100+hours))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, markerFile), []byte(filepath.Base(dir)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			when := time.Now().Add(-time.Duration(hours) * time.Hour)
			if err := os.Chtimes(dir, when, when); err != nil {
				t.Fatal(err)
			}
			l.others = append(l.others, dir)
		}
		if err := os.MkdirAll(l.legacy, 0o755); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-1000 * time.Hour)
		if err := os.Chtimes(l.legacy, old, old); err != nil {
			t.Fatal(err)
		}
		languages = append(languages, l)
	}
	if len(languages) == 0 {
		t.Skip("this build embeds no extension SDK")
	}

	// Ages 1, 2 and 3 hours are the three most recently used others; 4, 5 and 6 hours are evicted.
	check := func(when string, evicted bool) {
		t.Helper()
		for _, l := range languages {
			requireExists(t, l.own, when+": this build's own directory")
			requireExists(t, l.legacy, when+": the unhashed legacy directory")
			for _, dir := range l.others {
				var hours int
				if _, err := fmt.Sscanf(filepath.Base(dir)[len(filepath.Base(dir))-2:], "%x", &hours); err != nil {
					t.Fatal(err)
				}
				if evicted && hours > 3 {
					requireGone(t, dir, when+": a directory beyond the most recently used")
				} else {
					requireExists(t, dir, when+": a recently used directory")
				}
			}
		}
	}

	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	check("a warm start", false)

	// Removing this build's marker makes the next start stage again, which is what evicts.
	for _, l := range languages {
		if err := os.Remove(filepath.Join(l.own, markerFile)); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	check("a stage", true)
}
