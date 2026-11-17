//go:build !pig_strip_codemode

package cli

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type noBranch struct {
	extension.ReadonlySessionManager
}

func (noBranch) GetBranch(...string) []codingagent.SessionEntry { return nil }

// pig additive (D263): nothing selects wazero's interpreter for Code Mode by default. The codemode tool of the built-in extensions the pig command builds runs a
// script on the compiler: the compiler writes its machine code under the compilation cache directory the command gives the sandbox, and the interpreter writes none.
func TestPigCommandRunsCodeModeOnTheCompilerByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("TMPDIR", t.TempDir())
	cacheDir := wazeroCacheDir()
	if cacheDir == "" {
		t.Fatal("no compilation cache directory under PIG_HOME")
	}
	var factory extension.ExtensionFactory
	for _, inline := range nativeBuiltInExtensions(nil) {
		if named, ok := inline.(extension.NamedInlineExtension); ok && named.Name == "codemode" {
			factory = named.Factory
		}
	}
	if factory == nil {
		t.Fatal("the pig command builds no codemode extension")
	}
	loaded, err := factoryload.LoadExtensionFromFactory(factory, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "builtin:codemode")
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := loaded.RegisteredTool("codemode")
	if !ok {
		t.Fatal("the codemode extension registers no codemode tool")
	}
	base := extension.NewContext(t.TempDir(), nil, func() error { return nil }, extension.ContextActions{SessionManager: noBranch{}})
	ctx := extension.WithToolContext(t.Context(), extension.NewToolContext(base, "call", t.Context(), extension.ToolActions{}))
	params, _ := json.Marshal(map[string]string{"code": "return 6 * 7"})
	result, err := tool.Definition.Execute(ctx, "call", params, nil)
	if err != nil || result.IsError {
		t.Fatalf("the codemode tool failed: %v %+v", err, result)
	}
	var text strings.Builder
	for _, item := range result.Content {
		if content, ok := item.(ai.TextContent); ok {
			text.WriteString(content.Text)
		}
	}
	if !strings.Contains(text.String(), "42") {
		t.Fatalf("the script's result = %q, want 42", text.String())
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
	if files == 0 {
		t.Fatalf("the script ran and left nothing in the compilation cache %s: the sandbox did not run on the compiler", cacheDir)
	}
}
