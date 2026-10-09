package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
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
		suggestions := provider.GetSuggestions(context.Background(), []string{prefix}, 0, len(prefix), tui.AutocompleteSuggestionOptions{})
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
		suggestions := provider.GetSuggestions(context.Background(), []string{prefix}, 0, len(prefix), tui.AutocompleteSuggestionOptions{})
		if suggestions == nil || len(suggestions.Items) == 0 {
			t.Fatalf("no suggestions for %q", prefix)
		}
		if got := suggestions.Items[0].Description; got != want {
			t.Errorf("%s description = %q, want %q", prefix, got, want)
		}
	}
}

// Templates loaded from the session's prompt paths carry the sourceInfo Pi's resource loader gives them
// (resource-loader.ts updatePromptsFromPaths): the recorded package or settings metadata first, then the template
// loader's user/project/temporary classification (prompt-templates.ts getSourceInfo). A template under
// <agentDir>/prompts is [u], one under the project prompts directory [p], a package one [u:npm:…], another [t].
func TestAutocompleteProviderTagsTemplatesLoadedFromPromptPaths(t *testing.T) {
	mode, _ := newCustomEditorDispatchMode(t)
	mode.opts.AgentDir = t.TempDir()
	mode.opts.CWD = t.TempDir()
	write := func(path string) string {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("Template body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	user := write(filepath.Join(mode.opts.AgentDir, "prompts", "usertmpl.md"))
	project := write(filepath.Join(ProjectConfigDir(mode.opts.CWD), "prompts", "projtmpl.md"))
	packageRoot := filepath.Join(t.TempDir(), "pkg")
	pkg := write(filepath.Join(packageRoot, "prompts", "pkgtmpl.md"))
	extra := write(filepath.Join(t.TempDir(), "extratmpl.md"))
	mode.resourceSourceInfo = map[string]ResourceSourceInfo{
		pkg: {Path: pkg, ResourceType: "prompts", Enabled: true, Scope: "user", Origin: "package", Source: "npm:pkg", BaseDir: packageRoot},
	}
	mode.opts.PromptPaths = []string{user, project, pkg, extra}
	mode.loadPromptTemplates()
	provider := mode.buildAutocompleteProvider()
	for prefix, want := range map[string]string{
		"/usertmpl":  "[u] Template body",
		"/projtmpl":  "[p] Template body",
		"/pkgtmpl":   "[u:npm:pkg] Template body",
		"/extratmpl": "[t] Template body",
	} {
		suggestions := provider.GetSuggestions(context.Background(), []string{prefix}, 0, len(prefix), tui.AutocompleteSuggestionOptions{})
		if suggestions == nil || len(suggestions.Items) == 0 {
			t.Fatalf("no suggestions for %q", prefix)
		}
		if got := suggestions.Items[0].Description; got != want {
			t.Errorf("%s description = %q, want %q", prefix, got, want)
		}
	}
}

// interactive-mode.ts:710-727,793-799: an extension command named like a built-in gets a conflict warning that says it is skipped in
// autocomplete, and autocomplete does skip it; the name set is BUILTIN_SLASH_COMMANDS, which has no /debug, so an extension may use that.
func TestExtensionCommandsNamedLikeBuiltinsAreSkippedInAutocompleteAndWarned(t *testing.T) {
	mode, _ := newCustomEditorDispatchMode(t)
	mode.opts.AgentDir = t.TempDir()
	mode.newRunner = inproc.NewRunner([]extension.Extension{{
		Name:         "commands",
		CommandOrder: []string{"model", "debug", "greet"},
		Commands: map[string]extension.RegisteredCommand{
			"model": {Name: "model", Description: "Ext model", SourceInfo: PiSourceInfo{Path: "/ext/model.ts", Source: "local", Scope: "user", Origin: "top-level"}},
			"debug": {Name: "debug", Description: "Ext debug", SourceInfo: PiSourceInfo{Path: "/ext/debug.ts", Source: "local", Scope: "user", Origin: "top-level"}},
			"greet": {Name: "greet", Description: "Ext greet", SourceInfo: PiSourceInfo{Path: "/ext/greet.ts", Source: "local", Scope: "user", Origin: "top-level"}},
		},
	}}, mode.opts.AgentDir)
	diagnostics := mode.builtInCommandConflictDiagnostics()
	if len(diagnostics) != 1 || diagnostics[0].Path != "/ext/model.ts" || diagnostics[0].Message != "Extension command '/model' conflicts with built-in interactive command. Skipping in autocomplete." {
		t.Fatalf("conflict diagnostics = %+v, want one warning for /model only", diagnostics)
	}
	provider := mode.buildAutocompleteProvider()
	suggested := func(prefix, description string) bool {
		suggestions := provider.GetSuggestions(context.Background(), []string{prefix}, 0, len(prefix), tui.AutocompleteSuggestionOptions{})
		if suggestions == nil {
			return false
		}
		for _, item := range suggestions.Items {
			if item.Description == description {
				return true
			}
		}
		return false
	}
	if suggested("/model", "[u] Ext model") {
		t.Error("the extension /model is offered in autocomplete despite conflicting with the built-in")
	}
	if !suggested("/debug", "[u] Ext debug") || !suggested("/greet", "[u] Ext greet") {
		t.Error("an extension /debug or /greet is missing from autocomplete")
	}
}
