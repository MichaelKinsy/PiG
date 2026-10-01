package codingagent

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Ports the observable contract of getAutocompleteSourceTag and prefixAutocompleteDescription
// (.upstream/v0.99.1/packages/coding-agent/src/modes/interactive/interactive-mode.ts:640-675). Upstream has no unit test for
// them; each case follows one branch. The builtin case is the 0.99.1 change: "Built-in extension commands are untagged,
// like built-in commands."
func TestAutocompleteSourceTag(t *testing.T) {
	for _, tc := range []struct {
		name string
		info *PiSourceInfo
		want string
	}{
		{"no source info", nil, ""},
		{"builtin extension", &PiSourceInfo{Path: "builtin:mcp", Source: "builtin", Scope: "temporary", Origin: "top-level"}, ""},
		{"user auto", &PiSourceInfo{Source: "auto", Scope: "user"}, "u"},
		{"project local", &PiSourceInfo{Source: "local", Scope: "project"}, "p"},
		{"temporary cli", &PiSourceInfo{Source: "cli", Scope: "temporary"}, "t"},
		{"source is trimmed", &PiSourceInfo{Source: " cli ", Scope: "user"}, "u"},
		{"user npm", &PiSourceInfo{Source: "npm:@scope/pkg@1.2.3", Scope: "user"}, "u:npm:@scope/pkg@1.2.3"},
		{"project git", &PiSourceInfo{Source: "git:github.com/user/repo@v1", Scope: "project"}, "p:git:github.com/user/repo@v1"},
		{"git without ref", &PiSourceInfo{Source: "https://github.com/user/repo", Scope: "user"}, "u:git:github.com/user/repo"},
		{"unparsed source", &PiSourceInfo{Source: "inline", Scope: "temporary"}, "t"},
		{"unknown scope", &PiSourceInfo{Source: "local", Scope: "extra"}, "t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := autocompleteSourceTag(tc.info); got != tc.want {
				t.Fatalf("autocompleteSourceTag = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPrefixAutocompleteDescription(t *testing.T) {
	user := &PiSourceInfo{Source: "local", Scope: "user"}
	for _, tc := range []struct {
		name, description string
		info              *PiSourceInfo
		want              string
	}{
		{"described", "Run it", user, "[u] Run it"},
		{"undescribed", "", user, "[u]"},
		{"untagged", "Run it", nil, "Run it"},
		{"untagged and undescribed", "", nil, ""},
		{"builtin", "Manage MCP servers", &PiSourceInfo{Source: "builtin", Scope: "temporary"}, "Manage MCP servers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := prefixAutocompleteDescription(tc.description, tc.info); got != tc.want {
				t.Fatalf("prefixAutocompleteDescription = %q, want %q", got, tc.want)
			}
		})
	}
}

// createBaseAutocompleteProvider tags prompt template, extension command and skill entries with their source
// (interactive-mode.ts:743-777) and leaves built-in extension commands untagged.
func TestAutocompleteProviderTagsResourceCommands(t *testing.T) {
	mode, _ := newCustomEditorDispatchMode(t)
	mode.opts.AgentDir = t.TempDir()
	mode.promptTemplates = []PromptTemplate{{Name: "tmpl", Description: "Template desc", SourceInfo: PiSourceInfo{Source: "local", Scope: "user"}}}
	mode.opts.Skills = []*SkillDef{{Name: "sk", Description: "Skill desc", SourceInfo: PiSourceInfo{Source: "npm:pkg@1", Scope: "project"}}}
	mode.newRunner = inproc.NewRunner([]extension.Extension{{
		Name:         "commands",
		CommandOrder: []string{"tagged", "untagged", "bare"},
		Commands: map[string]extension.RegisteredCommand{
			"tagged":   {Name: "tagged", Description: "Tagged desc", SourceInfo: PiSourceInfo{Path: "/ext/tagged.ts", Source: "local", Scope: "project", Origin: "top-level"}},
			"untagged": {Name: "untagged", Description: "Built-in desc", SourceInfo: PiSourceInfo{Path: "builtin:mcp", Source: "builtin", Scope: "temporary", Origin: "top-level"}},
			"bare":     {Name: "bare"},
		},
	}}, mode.opts.AgentDir)
	provider := mode.buildAutocompleteProvider()
	described := func(prefix string) string {
		t.Helper()
		suggestions := provider.GetSuggestions([]string{prefix}, 0, len(prefix))
		if suggestions == nil || len(suggestions.Items) == 0 {
			t.Fatalf("no suggestions for %q", prefix)
		}
		return suggestions.Items[0].Description
	}
	for prefix, want := range map[string]string{
		"/tmpl":     "[u] Template desc",
		"/skill:sk": "[p:npm:pkg@1] Skill desc",
		"/tagged":   "[p] Tagged desc",
		"/untagged": "Built-in desc",
	} {
		if got := described(prefix); got != want {
			t.Errorf("%s description = %q, want %q", prefix, got, want)
		}
	}
}

// Loaded prompt templates carry their file path, not a SourceInfo (LoadPromptTemplates). Upstream stamps every template
// with the resource loader's sourceInfo, so a template under <agentDir>/prompts is tagged [u] and one from a settings or
// CLI path [t], exactly as pi.getCommands() reports their provenance.
func TestAutocompleteProviderTagsLoadedPromptTemplatesByPath(t *testing.T) {
	mode, _ := newCustomEditorDispatchMode(t)
	mode.opts.AgentDir = t.TempDir()
	mode.opts.CWD = t.TempDir()
	extra := filepath.Join(t.TempDir(), "extra.md")
	mode.promptTemplates = []PromptTemplate{
		{Name: "usertmpl", Description: "User template", FilePath: filepath.Join(mode.opts.AgentDir, "prompts", "usertmpl.md"), Scope: "user"},
		{Name: "extratmpl", Description: "Extra template", FilePath: extra, Scope: "extra"},
	}
	provider := mode.buildAutocompleteProvider()
	for prefix, want := range map[string]string{"/usertmpl": "[u] User template", "/extratmpl": "[t] Extra template"} {
		suggestions := provider.GetSuggestions([]string{prefix}, 0, len(prefix))
		if suggestions == nil || len(suggestions.Items) == 0 {
			t.Fatalf("no suggestions for %q", prefix)
		}
		if got := suggestions.Items[0].Description; got != want {
			t.Errorf("%s description = %q, want %q", prefix, got, want)
		}
	}
}
