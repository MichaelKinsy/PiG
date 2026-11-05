package codemode

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pig additive (D263): Options.Interpreter reaches the sandbox the tool runs a script in. The compiler writes its machine code under
// CacheDir and the interpreter writes nothing there, so the directory shows which runtime ran the script.
func TestExecuteOptionInterpreterSelectsTheInterpreterRuntime(t *testing.T) {
	for _, interpreter := range []bool{false, true} {
		useSaveTempDir(t)
		cacheDir := t.TempDir()
		base := extension.NewContext(t.TempDir(), nil, func() error { return nil }, extension.ContextActions{SessionManager: emptyBranch{}})
		ctx := extension.WithToolContext(context.Background(), extension.NewToolContext(base, "call", context.Background(), extension.ToolActions{}))
		params, _ := json.Marshal(map[string]string{"code": "return 6 * 7"})
		result, err := Execute(ctx, "call", params, nil, Options{CacheDir: cacheDir, Interpreter: interpreter})
		if err != nil {
			t.Fatal(err)
		}
		if text := resultText(result); result.IsError || !strings.Contains(text, "42") {
			t.Fatalf("interpreter=%v: result = %q (error %v), want 42", interpreter, text, result.IsError)
		}
		files := 0
		if err := filepath.WalkDir(cacheDir, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				files++
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if written := files > 0; written == interpreter {
			t.Errorf("interpreter=%v: %d files in CacheDir", interpreter, files)
		}
	}
}
