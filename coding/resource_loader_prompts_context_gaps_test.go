package coding

import (
	"path/filepath"
	"slices"
	"testing"
)

func contextPaths(files []ContextFile) []string {
	var out []string
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}

// resource-loader.ts reload: context files are the agent directory's and each ancestor's AGENTS.md (user first), noContextFiles disables discovery, agentsFilesOverride receives and replaces the discovered list, and the getters return a copy of what reload stored.
// Pi: packages/coding-agent/src/core/resource-loader.ts getAgentsFiles (:144), reload (:373-379).
func TestDefaultResourceLoaderAgentsFilesFollowReloadOptionsAndOverride(t *testing.T) {
	f := newResourceLoaderFixture(t)
	userFile := filepath.Join(f.agentDir, "AGENTS.md")
	repoFile := filepath.Join(f.repo, "AGENTS.md")
	cwdFile := filepath.Join(f.cwd, "AGENTS.md")
	writeResource(t, userFile, "user rules")
	writeResource(t, repoFile, "repo rules")
	writeResource(t, cwdFile, "cwd rules")

	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir})
	var contract ResourceLoader = loader
	if got := contract.GetAgentsFiles().AgentsFiles; len(got) != 0 {
		t.Fatalf("a loader that has not reloaded reports context files %v", contextPaths(got))
	}
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if got, want := contextPaths(contract.GetAgentsFiles().AgentsFiles), []string{userFile, repoFile, cwdFile}; !slices.Equal(got, want) {
		t.Fatalf("context files = %v, want %v (user, then ancestors outermost first)", got, want)
	}
	stored := contract.GetAgentsFiles().AgentsFiles
	if stored[0].Content != "user rules" {
		t.Fatalf("content = %q", stored[0].Content)
	}
	stored[0].Content = "mutated"
	if contract.GetAgentsFiles().AgentsFiles[0].Content != "user rules" {
		t.Fatal("GetAgentsFiles exposes the loader's own slice")
	}

	off := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, NoContextFiles: true})
	if err := off.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := off.GetAgentsFiles().AgentsFiles; got == nil || len(got) != 0 {
		t.Fatalf("NoContextFiles reported %#v, want an empty non-nil list", got)
	}

	var seen []string
	over := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, AgentsFilesOverride: func(base AgentsFilesResult) AgentsFilesResult {
		seen = contextPaths(base.AgentsFiles)
		return AgentsFilesResult{AgentsFiles: []ContextFile{{Path: "/virtual/AGENTS.md", Content: "virtual"}}}
	}})
	if err := over.Reload(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []string{userFile, repoFile, cwdFile}) {
		t.Fatalf("the override saw %v", seen)
	}
	if got := contextPaths(over.GetAgentsFiles().AgentsFiles); !slices.Equal(got, []string{"/virtual/AGENTS.md"}) {
		t.Fatalf("override result = %v", got)
	}
	disabled := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, NoContextFiles: true, AgentsFilesOverride: func(base AgentsFilesResult) AgentsFilesResult {
		seen = contextPaths(base.AgentsFiles)
		return base
	}})
	if err := disabled.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Fatalf("with NoContextFiles the override still saw %v", seen)
	}
}

// resource-loader.ts reload: the system prompt is the option, else the discovered SYSTEM.md; systemPromptOverride receives it (nil when there is none) and its result is what the getter reports; the appended prompts are the option, else the discovered APPEND_SYSTEM.md, empty entries dropped, passed through appendSystemPromptOverride, and returned as a copy.
// Pi: packages/coding-agent/src/core/resource-loader.ts getSystemPrompt (:151), getAppendSystemPrompt (:156), reload (:381-399).
func TestDefaultResourceLoaderSystemPromptsFollowOptionsAndOverrides(t *testing.T) {
	f := newResourceLoaderFixture(t)
	newLoader := func(options DefaultResourceLoaderOptions) *DefaultResourceLoader {
		options.CWD, options.AgentDir = f.cwd, f.agentDir
		loader := NewDefaultResourceLoader(options)
		if err := loader.Reload(); err != nil {
			t.Fatal(err)
		}
		return loader
	}

	var none ResourceLoader = newLoader(DefaultResourceLoaderOptions{})
	if text, ok := none.GetSystemPrompt(); ok || text != "" {
		t.Fatalf("no SYSTEM.md: prompt %q, %v", text, ok)
	}
	if got := none.GetAppendSystemPrompt(); got == nil || len(got) != 0 {
		t.Fatalf("no APPEND_SYSTEM.md: %#v, want an empty non-nil list", got)
	}
	if _, ok := none.GetSystemPromptSource(); ok {
		t.Fatal("no SYSTEM.md but a prompt source")
	}

	writeResource(t, filepath.Join(f.agentDir, "SYSTEM.md"), "discovered system")
	writeResource(t, filepath.Join(f.agentDir, "APPEND_SYSTEM.md"), "discovered append")
	var discovered ResourceLoader = newLoader(DefaultResourceLoaderOptions{})
	if text, ok := discovered.GetSystemPrompt(); !ok || text != "discovered system" {
		t.Fatalf("discovered system prompt = %q, %v", text, ok)
	}
	if got := discovered.GetAppendSystemPrompt(); !slices.Equal(got, []string{"discovered append"}) {
		t.Fatalf("discovered append prompt = %v", got)
	}
	appended := discovered.GetAppendSystemPrompt()
	appended[0] = "mutated"
	if discovered.GetAppendSystemPrompt()[0] != "discovered append" {
		t.Fatal("GetAppendSystemPrompt exposes the loader's own slice")
	}
	sources := discovered.GetAppendSystemPromptSources()
	if len(sources) != 1 {
		t.Fatalf("append sources = %v", sources)
	}
	sources[0].Path = "mutated"
	if discovered.GetAppendSystemPromptSources()[0].Path == "mutated" {
		t.Fatal("GetAppendSystemPromptSources exposes the loader's own slice")
	}

	var optioned ResourceLoader = newLoader(DefaultResourceLoaderOptions{SystemPrompt: new("literal system"), AppendSystemPrompt: []string{"one", "", "two"}})
	if text, ok := optioned.GetSystemPrompt(); !ok || text != "literal system" {
		t.Fatalf("option system prompt = %q, %v (it wins over SYSTEM.md)", text, ok)
	}
	if got := optioned.GetAppendSystemPrompt(); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("option append prompts = %v, want the empty entry dropped", got)
	}
	empty := newLoader(DefaultResourceLoaderOptions{AppendSystemPrompt: []string{}})
	if got := empty.GetAppendSystemPrompt(); len(got) != 0 {
		t.Fatalf("an explicit empty append list still read APPEND_SYSTEM.md: %v", got)
	}

	var gotBase *string
	overridden := newLoader(DefaultResourceLoaderOptions{
		SystemPromptOverride: func(base *string) *string {
			gotBase = base
			return new("overridden " + *base)
		},
		AppendSystemPromptOverride: func(base []string) []string { return append(slices.Clone(base), "extra") },
	})
	if gotBase == nil || *gotBase != "discovered system" {
		t.Fatalf("systemPromptOverride received %v", gotBase)
	}
	if text, ok := overridden.GetSystemPrompt(); !ok || text != "overridden discovered system" {
		t.Fatalf("overridden system prompt = %q, %v", text, ok)
	}
	if got := overridden.GetAppendSystemPrompt(); !slices.Equal(got, []string{"discovered append", "extra"}) {
		t.Fatalf("overridden append prompts = %v", got)
	}
	if source, ok := overridden.GetSystemPromptSource(); !ok || source.Path != filepath.Join(f.agentDir, "SYSTEM.md") {
		t.Fatalf("an override changed the system prompt source: %+v, %v", source, ok)
	}
}
