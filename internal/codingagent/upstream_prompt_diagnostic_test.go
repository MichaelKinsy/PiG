package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:628
func TestUpstreamPromptTemplateInvalidYAMLDiagnostic(t *testing.T) {
	t.Run("reports invalid YAML frontmatter and keeps valid siblings", func(t *testing.T) {
		dir := t.TempDir()
		invalid := filepath.Join(dir, "invalid.md")
		for name, content := range map[string]string{"invalid.md": "---\ndescription: Broken: unquoted colon\n---\nDo something.\n", "valid.md": "Valid prompt content."} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		result := LoadPromptTemplates("", "", dir)
		if len(result.Templates) != 1 || result.Templates[0].Name != "valid" {
			t.Fatalf("templates = %#v", result.Templates)
		}
		if len(result.Diagnostics) != 1 {
			t.Fatalf("diagnostics = %#v", result.Diagnostics)
		}
		diagnostic := result.Diagnostics[0]
		if diagnostic.Type != "warning" || diagnostic.Path != invalid || !strings.Contains(diagnostic.Message, "line 1, column 14") {
			t.Fatalf("diagnostic = %#v; want warning at %s containing line 1, column 14", diagnostic, invalid)
		}
	})
}
