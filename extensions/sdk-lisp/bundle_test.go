package lispsdk

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The pack carries exactly the sources on disk: after an edit to src/*.lisp, LICENSE or pig-sdk.asd, run `go generate ./extensions/sdk-lisp`.
func TestPackMatchesSource(t *testing.T) {
	files, err := filepath.Glob("src/*.lisp")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "LICENSE", "pig-sdk.asd")
	slices.Sort(files)
	if got := BundledFiles(); !slices.Equal(got, files) {
		t.Fatalf("bundled files %v, sources %v: run go generate ./extensions/sdk-lisp", got, files)
	}
	digest := sha256.New()
	digest.Write([]byte("staging-v2\x00"))
	for _, name := range files {
		want, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Source.ReadFile(name)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s differs from the pack (%v): run go generate ./extensions/sdk-lisp", name, err)
		}
		fmt.Fprintf(digest, "%s\x00%d\x00", name, len(want))
		digest.Write(want)
	}
	if want := hex.EncodeToString(digest.Sum(nil))[:16]; StagedHash() != want {
		t.Fatalf("staged hash %s, sources hash to %s: run go generate ./extensions/sdk-lisp", StagedHash(), want)
	}
}

// Open serves what ReadFile does, with the size the staging hash reads.
func TestOpenServesTheFile(t *testing.T) {
	f, err := Source.Open("pig-sdk.asd")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	if err != nil || int64(len(data)) != info.Size() || len(data) == 0 {
		t.Fatalf("read %d bytes, size %d, %v", len(data), info.Size(), err)
	}
	if _, err := Source.Open("src/missing.lisp"); err == nil {
		t.Fatal("a file outside the pack opened")
	}
}
