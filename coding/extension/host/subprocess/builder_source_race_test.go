package subprocess

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/buildprogress"
)

// The cache key of a build is the hash of the source as it was read before the build started. The compiler reads the files later,
// after a wait for a peer's build lock and then for its own compile, so a source edited in that time was compiled into an
// artifact that the key does not describe. Publishing it makes the key serve the edited build for the original source for good:
// the author reverts the edit and gets the binary of the edit.
func TestBuilderPublishesNothingWhenTheSourceChangesDuringTheBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a Go extension")
	}
	const before, after = "SOURCE-BEFORE-THE-EDIT", "SOURCE-AFTER-THE-EDIT"
	srcDir := t.TempDir()
	writeSource := func(marker string) {
		t.Helper()
		source := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"" + marker + "\") }\n"
		if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module racing-ext\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSource(before)
	builder := NewBuilder(t.TempDir())

	// The author saves an edit after the build hashed the source and before the compiler reads it.
	edited := false
	racing := buildprogress.Observe(context.Background(), func(event buildprogress.Event) {
		if event.Phase == "Compiling Go member" && !edited {
			edited = true
			writeSource(after)
		}
	}, false)
	_, racedErr := builder.BuildContext(racing, "racing", srcDir)
	if !edited {
		t.Fatal("the edit never ran: the build did not reach the compiler")
	}

	// The author puts the original source back and starts again.
	writeSource(before)
	result, err := builder.Build("racing", srcDir)
	if err != nil {
		t.Fatalf("build of the original source: %v", err)
	}
	binary, err := os.ReadFile(result.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(binary, []byte(after)) {
		t.Fatalf("the original source is served the binary of the edit (cached=%t, build that raced the edit: %v)", result.Cached, racedErr)
	}
	if !bytes.Contains(binary, []byte(before)) {
		t.Fatalf("binary of the original source holds neither marker (cached=%t)", result.Cached)
	}
	if racedErr == nil || !strings.Contains(racedErr.Error(), "changed while") {
		t.Fatalf("build that raced the edit = %v; want an error that says the source changed while it built", racedErr)
	}
}
