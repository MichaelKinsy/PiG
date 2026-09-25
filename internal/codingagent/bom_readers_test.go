package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
)

// utf8BOM is the byte order mark upstream readers strip with utils/text.ts
// stripBom before parsing.
const utf8BOM = "\xef\xbb\xbf"

func writeBOMFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(utf8BOM+content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Upstream settings-manager.ts parses JSON.parse(stripBom(content)).
func TestSettingsFileWithBOMLoads(t *testing.T) {
	agentDir := t.TempDir()
	writeBOMFile(t, filepath.Join(agentDir, "settings.json"), `{"theme":"light"}`)
	sm := NewSettingsManager(t.TempDir(), agentDir)
	if errs := sm.DrainErrors(); len(errs) != 0 {
		t.Fatalf("settings errors = %+v", errs)
	}
	if got := sm.GetGlobalSettings().Theme; got != "light" {
		t.Fatalf("theme = %q, want light", got)
	}
}

// Upstream model-config.ts parses stripJsonComments(stripBom(content)).
func TestModelsJSONWithBOMLoads(t *testing.T) {
	agentDir := t.TempDir()
	writeBOMFile(t, filepath.Join(agentDir, "models.json"), `{"providers":{}}`)
	if msg := NewModelRegistry(agentDir).LoadError(); msg != "" {
		t.Fatalf("models.json load error = %q", msg)
	}
}

// Upstream trust-manager.ts parses JSON.parse(stripBom(readFileSync(path))).
func TestTrustStoreWithBOMLoads(t *testing.T) {
	agentDir := t.TempDir()
	writeBOMFile(t, filepath.Join(agentDir, "trust.json"), `{}`)
	if _, err := NewProjectTrustStore(agentDir).Get(t.TempDir()); err != nil {
		t.Fatalf("trust store read: %v", err)
	}
}

// Upstream resource-loader.ts strips the mark from context file content.
func TestContextFileContentStripsBOM(t *testing.T) {
	dir := t.TempDir()
	writeBOMFile(t, filepath.Join(dir, "AGENTS.md"), "rules")
	files := LoadProjectContextFiles(dir, "")
	if len(files) != 1 || files[0].Content != "rules" {
		t.Fatalf("context files = %+v", files)
	}
}

// Upstream file-processor.ts wraps stripBom(readFile(...)) for text files.
func TestProcessCLIFileArgumentsStripsBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	writeBOMFile(t, path, "hello\n")
	got, err := ProcessCLIFileArguments([]string{"note.txt"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := `<file name="` + path + `">` + "\nhello\n</file>\n"
	if got.Text != want {
		t.Fatalf("Text = %q, want %q", got.Text, want)
	}
}

// Upstream utils/frontmatter.ts strips the mark before looking for "---".
func TestFrontmatterParseStripsBOM(t *testing.T) {
	doc := frontmatter.Parse(utf8BOM + "---\ndescription: hi\n---\nbody")
	if got := doc.String("description"); got != "hi" {
		t.Fatalf("description = %q, want hi", got)
	}
}
