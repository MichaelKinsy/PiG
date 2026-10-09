// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

func TestDiffShowsOnlyTheChange(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n"
	after := "a\nb\nc\nd\nE\nf\ng\nh\ni\nj\nk\nl\nm\n"
	want := "--- a/x.go\n+++ b/x.go\n@@ -2,7 +2,7 @@\n b\n c\n d\n-e\n+E\n f\n g\n h\n@@ -10,3 +10,4 @@\n j\n k\n l\n+m\n"
	if got := upgrade.Diff("x.go", []byte(before), []byte(after)); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if upgrade.Diff("x.go", []byte(before), []byte(before)) != "" {
		t.Fatal("an unchanged file has a diff")
	}
}

func TestDiffOfInsertionAndDeletion(t *testing.T) {
	got := upgrade.Diff("x.go", []byte("one\ntwo\n"), []byte("one\ninserted\ntwo\n"))
	if !strings.Contains(got, "+inserted") || strings.Contains(got, "-") && strings.Contains(got, "\n-t") {
		t.Fatalf("got:\n%s", got)
	}
	got = upgrade.Diff("x.go", []byte("one\ntwo\nthree\n"), []byte("one\nthree\n"))
	if !strings.Contains(got, "-two") || strings.Contains(got, "+three") {
		t.Fatalf("got:\n%s", got)
	}
}
