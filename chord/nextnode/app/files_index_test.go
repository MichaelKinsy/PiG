package app

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/node/app/files-index.test.ts: the files index's rules on their own (decision "Files v2"):
// space names, paths, the folder a path is in. The index's tables, queries and changes are tested through `Files` in
// files_test.go.

func TestFilesIndexTakesSpaceNamesOfLowerCaseDigitsDotUnderscoreAndDashAtMost64(t *testing.T) {
	for _, space := range []string{"shared", "artifacts", "a.b_c-1", strings.Repeat("x", 64)} {
		if err := CheckSpace(space); err != nil {
			t.Fatalf("CheckSpace(%q) = %v", space, err)
		}
	}
	for _, space := range []string{"", "Shared", "-a", "a/b", "a b", strings.Repeat("x", 65)} {
		err := CheckSpace(space)
		if code, _ := chordCode(err); code != data.CodeInvalid {
			t.Fatalf("CheckSpace(%q) = %v, want invalid", space, err)
		}
	}
}

func TestFilesIndexTakesPathsOfNonEmptySegmentsWithoutDotOrDotDotTheRootOnlyAsAFolder(t *testing.T) {
	for _, path := range []string{"a", "docs/report.md", "a b/ü/.hidden", strings.Repeat("x", 1024)} {
		if err := CheckPath(path, PathFile); err != nil {
			t.Fatalf("CheckPath(%q) = %v", path, err)
		}
	}
	for _, path := range []string{"", "/a", "a/", "a//b", ".", "..", "a/./b", "a/../b", "a\x00b", strings.Repeat("x", 1025)} {
		if code, _ := chordCode(CheckPath(path, PathFile)); code != data.CodeInvalid {
			t.Fatalf("CheckPath(%q) was not refused", path)
		}
	}
	if err := CheckPath("", PathFolder); err != nil {
		t.Fatal(err)
	}
}

func TestFilesIndexNamesTheFolderAPathIsIn(t *testing.T) {
	for path, want := range map[string]string{"a": "", "docs/a": "docs", "docs/sub/a": "docs/sub"} {
		if got := ParentOf(path); got != want {
			t.Fatalf("ParentOf(%q) = %q, want %q", path, got, want)
		}
	}
}
