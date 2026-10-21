package runtimecell

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/buildprogress"
)

// copySDKTree copies the repository's Go SDK, a stand-in for a checkout an author points PIG_SDK_GO_ROOT at and keeps editing.
func copySDKTree(t *testing.T) string {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	return destination
}

// A cell's digest covers the SDK tree as it was hashed before the build; the compiler reads it later. An SDK file the author saves in
// between is compiled into a cell that its digest does not describe, so the cell is not published.
func TestBuildGoPackedCellPublishesNothingWhenTheSDKChangesDuringTheBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a packed Go cell")
	}
	extension := goExtensionFixture(t, "racing", `sdk.New("racing")`)
	sdkRoot := copySDKTree(t)
	t.Setenv("PIG_SDK_GO_ROOT", sdkRoot)
	cacheRoot := filepath.Join(t.TempDir(), "cache")

	edited := false
	racing := buildprogress.Observe(context.Background(), func(event buildprogress.Event) {
		if event.Phase != "Compiling Go members" || edited {
			return
		}
		edited = true
		target := filepath.Join(sdkRoot, "doc.go")
		data, err := os.ReadFile(target)
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(target, append(data, []byte("\n// saved while the cell was building\n")...), 0o644); err != nil {
			t.Error(err)
		}
	}, false)
	cell, err := BuildGoPackedCell(racing, cacheRoot, "racing-cell", []GoExtension{extension})
	if !edited {
		t.Fatal("the edit never ran: the build did not reach the compiler")
	}
	if err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("build that raced an SDK edit = %+v, %v; want an error that says the inputs changed while it built", cell, err)
	}
	if entries, readErr := os.ReadDir(filepath.Join(cacheRoot, "cells", "go")); readErr == nil {
		for _, entry := range entries {
			if _, ok := readReadyMetadata(filepath.Join(cacheRoot, "cells", "go", entry.Name())); ok {
				t.Fatalf("cell %s was published although its SDK changed during the build", entry.Name())
			}
		}
	}
}
