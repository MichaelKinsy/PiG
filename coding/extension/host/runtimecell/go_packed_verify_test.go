package runtimecell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/buildprogress"
)

// A cell's extension sources enter its digest only as the Hash their caller derived from them, so the cell cannot read them again
// itself. The caller's Verify says whether they still hash to it, and the cell is not published when they do not.
func TestBuildGoPackedCellPublishesNothingWhenAMembersSourceChangesDuringTheBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a packed Go cell")
	}
	extension := goExtensionFixture(t, "racing", `sdk.New("SOURCE-BEFORE-THE-EDIT")`)
	extension.Hash = hashTree(extension.Root)
	extension.Verify = func() error {
		if hashTree(extension.Root) != extension.Hash {
			return errors.New("the source of racing changed while its cell was being built")
		}
		return nil
	}
	cacheRoot := filepath.Join(t.TempDir(), "cache")

	edited := false
	racing := buildprogress.Observe(context.Background(), func(event buildprogress.Event) {
		if event.Phase != "Compiling Go members" || edited {
			return
		}
		edited = true
		target := filepath.Join(extension.Root, "extension.go")
		data, err := os.ReadFile(target)
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(target, []byte(strings.Replace(string(data), "BEFORE", "AFTER", 1)), 0o644); err != nil {
			t.Error(err)
		}
	}, false)
	cell, err := BuildGoPackedCell(racing, cacheRoot, "racing-cell", []GoExtension{extension})
	if !edited {
		t.Fatal("the edit never ran: the build did not reach the compiler")
	}
	if err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("build that raced a source edit = %+v, %v; want an error that says the source changed while it built", cell, err)
	}
	if entries, readErr := os.ReadDir(filepath.Join(cacheRoot, "cells", "go")); readErr == nil {
		for _, entry := range entries {
			if _, ok := readReadyMetadata(filepath.Join(cacheRoot, "cells", "go", entry.Name())); ok {
				t.Fatalf("cell %s was published although its source changed during the build", entry.Name())
			}
		}
	}

	// The unedited source builds, and the check passes when nothing changed.
	extension.Hash = hashTree(extension.Root)
	cell, err = BuildGoPackedCell(context.Background(), cacheRoot, "racing-cell", []GoExtension{extension})
	if err != nil || cell.Cached {
		t.Fatalf("build after the edit = %+v, %v; want a fresh build of the current source", cell, err)
	}
}
