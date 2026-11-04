package text_test

// pi: packages/coding-agent/src/utils/text.ts

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// Ports Pi test/suite/regressions/8337-utf8-bom-parsing.test.ts "loads frontmatter and settings with a leading BOM": splitBom, the
// frontmatter parser and both settings files accept a leading BOM, and a settings write does not put the BOM back.
func TestLoadsFrontmatterAndSettingsWithALeadingBOM(t *testing.T) {
	if mark, rest := text.SplitBom("\uFEFFcontent"); mark != "\uFEFF" || rest != "content" {
		t.Fatalf("SplitBom = (%q, %q), want (BOM, content)", mark, rest)
	}
	document := "---\nname: demo\ndescription: Test\n---\nBody"
	doc := frontmatter.Parse("\uFEFF" + document)
	if want := map[string]any{"name": "demo", "description": "Test"}; !reflect.DeepEqual(doc.Frontmatter, want) || doc.Body != "Body" || doc.Err != nil {
		t.Fatalf("Parse = %#v (body %q, err %v), want %#v and body Body", doc.Frontmatter, doc.Body, doc.Err, want)
	}

	testDir := t.TempDir()
	agentDir := filepath.Join(testDir, "agent")
	projectDir := filepath.Join(testDir, "project")
	for _, dir := range []string{agentDir, codingagent.ProjectConfigDir(projectDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	globalSettingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(globalSettingsPath, []byte("\uFEFF"+`{"defaultModel":"global-model"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codingagent.ProjectConfigDir(projectDir), "settings.json"), []byte("\uFEFF"+`{"defaultProvider":"project-provider"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	settings := codingagent.NewSettingsManager(projectDir, agentDir)
	if got := settings.GetDefaultModel(); got != "global-model" {
		t.Fatalf("default model = %q, want global-model", got)
	}
	if got := settings.GetDefaultProvider(); got != "project-provider" {
		t.Fatalf("default provider = %q, want project-provider", got)
	}
	if err := settings.SetTheme("dark"); err != nil {
		t.Fatal(err)
	}
	if err := settings.Flush(); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(globalSettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(string(written), "\uFEFF") {
		t.Fatalf("settings write kept the BOM: %q", written)
	}
}
