package tools

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/env"
)

// Not upstream cases (tools.test.ts has none for path-utils.ts): they pin
// normalizeToolPath and the macOS filename variants resolveReadToolPath tries.

func TestToolPathsDropALeadingAtSignAndNormalizeUnicodeSpaces(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "a b.txt", "text")
	got := must(resolveToolPath(background, executionEnv, "@a\u00a0b.txt"))
	if want := filepath.Join(executionEnv.Cwd(), "a b.txt"); got != want {
		t.Fatalf("resolved = %q, want %q", got, want)
	}
}

func TestReadFindsTheMacOSVariantOfAMissingName(t *testing.T) {
	cases := []struct{ name, existing, requested string }{
		{"narrow no-break space before PM", "Screenshot 10\u202fPM.png", "Screenshot 10 PM.png"},
		{"curly apostrophe", "it\u2019s.txt", "it's.txt"},
		{"decomposed characters", "caf\u0065\u0301.txt", "caf\u00e9.txt"},
		{"decomposed characters and a curly apostrophe", "caf\u0065\u0301\u2019.txt", "caf\u00e9'.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executionEnv := createEnv(t)
			writeText(t, executionEnv, tc.existing, "text")
			result := mustRun(t, CreateReadTool(), map[string]any{"path": tc.requested}, executionEnv)
			if got := textOutput(result.ToolExecutionResult); got != "text" {
				t.Fatalf("read %q = %q", tc.requested, got)
			}
		})
	}
}

func TestReadKeepsTheRequestedPathWhenNoVariantExists(t *testing.T) {
	executionEnv := createEnv(t)
	_, err := run(CreateReadTool(), map[string]any{"path": "missing.txt"}, executionEnv, background)
	if err == nil || !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("error = %v", err)
	}
}

func TestEditRejectsAPathThatIsNotAFile(t *testing.T) {
	executionEnv := createEnv(t)
	mustDo(t, executionEnv.CreateDir(background, "dir", nil))
	_, err := run(CreateEditTool(), map[string]any{"path": "dir", "edits": []any{map[string]any{"oldText": "a", "newText": "b"}}}, executionEnv, background)
	expectFailure(t, err, "^Could not edit file: dir. Path is not a file.$")
	_, err = run(CreateEditTool(), map[string]any{"path": "missing.txt", "edits": []any{map[string]any{"oldText": "a", "newText": "b"}}}, executionEnv, background)
	expectFailure(t, err, `^Could not edit file: missing.txt. Error code: not_found.$`)
	_, err = run(CreateEditTool(), map[string]any{"path": "dir", "edits": []any{}}, executionEnv, background)
	expectFailure(t, err, "^Edit tool input is invalid. edits must contain at least one replacement.$")
}

// canonicalFailsEnv fails CanonicalPath with a fixed error.
type canonicalFailsEnv struct {
	*slowReadEnv
	err error
}

func (failing *canonicalFailsEnv) CanonicalPath(context.Context, string) (string, error) {
	return "", failing.err
}

// Not an upstream case: file-mutation-queue.ts treats only not_found as a
// missing file and not_supported as a file system without canonical paths; any
// other failure surfaces.
func TestMutationQueueSurfacesCanonicalPathFailuresOtherThanNotFound(t *testing.T) {
	denied := &env.FileError{Code: env.FileErrorPermissionDenied, Message: "denied"}
	failing := &canonicalFailsEnv{newSlowReadEnv(t.TempDir()), denied}
	_, err := run(CreateWriteTool(), map[string]any{"path": "file.txt", "content": "x"}, failing, background)
	if !errors.Is(err, denied) {
		t.Fatalf("error = %v, want the permission failure", err)
	}
	unsupported := &canonicalFailsEnv{newSlowReadEnv(t.TempDir()), &env.FileError{Code: env.FileErrorNotSupported, Message: "no canonical paths"}}
	mustRun(t, CreateWriteTool(), map[string]any{"path": "file.txt", "content": "x"}, unsupported)
	if got := readText(t, unsupported, "file.txt"); got != "x" {
		t.Fatalf("file = %q", got)
	}
}
