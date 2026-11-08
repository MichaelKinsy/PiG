package lispsdk

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// The pack lists exactly the sources on disk: after an edit to src/*.lisp, LICENSE or pig-sdk.asd, run `go generate ./extensions/sdk-lisp`. That the frame holds these files' text and StagedHash is their hash is checked where it can be decoded: TestEmbeddedBundlesHoldTheirSources in coding/extension/pigsdk.
func TestPackListsTheSources(t *testing.T) {
	files, err := filepath.Glob("src/*.lisp")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "LICENSE", "pig-sdk.asd")
	slices.Sort(files)
	if got := BundledFiles(); !slices.Equal(got, files) {
		t.Fatalf("bundled files %v, sources %v: run go generate ./extensions/sdk-lisp", got, files)
	}
}

func TestSourceHoldsTheFrame(t *testing.T) {
	entries, err := fs.ReadDir(Source, ".")
	if err != nil || len(entries) != 1 || entries[0].Name() != "sdk.zst" {
		t.Fatalf("Source holds %v (%v), want the one file sdk.zst", entries, err)
	}
	frame, err := Source.ReadFile("sdk.zst")
	if err != nil || len(frame) == 0 {
		t.Fatalf("sdk.zst: %d bytes, %v", len(frame), err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(StagedHash()) {
		t.Fatalf("StagedHash() = %q, want 16 hex digits", StagedHash())
	}
}
