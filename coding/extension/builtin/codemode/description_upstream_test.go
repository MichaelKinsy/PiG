package codemode_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
)

// Ports the "codemode description catalog" cases of packages/coding-agent/test/tool-search.test.ts (v0.99.2).

func agentTool(name, description string) extension.AgentTool {
	return extension.AgentTool{Name: name, Label: name, Description: description, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}
}

func catalogFixture() (all []extension.AgentTool, namespaces map[string]extension.ToolNamespace, github []extension.AgentTool) {
	plain := agentTool("read_notes", "Read notes.")
	for _, suffix := range []string{"a", "b", "c"} {
		github = append(github, agentTool("mcp__github__"+suffix, "GitHub "+suffix+"."))
	}
	docs := []extension.AgentTool{agentTool("mcp__docs__search", "Search docs."), agentTool("mcp__docs__long", strings.Repeat("Long ", 200))}
	all = append(append([]extension.AgentTool{plain}, github...), docs...)
	namespaces = map[string]extension.ToolNamespace{}
	for _, tool := range github {
		namespaces[tool.Name] = extension.ToolNamespace{Name: "mcp__github", Description: "GitHub server"}
	}
	for _, tool := range docs {
		namespaces[tool.Name] = extension.ToolNamespace{Name: "mcp__docs"}
	}
	return all, namespaces, github
}

func mustContain(t *testing.T, description string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(description, want) {
			t.Errorf("description lacks %q", want)
		}
	}
}

func mustNotContain(t *testing.T, description string, unwanted ...string) {
	t.Helper()
	for _, s := range unwanted {
		if strings.Contains(description, s) {
			t.Errorf("description contains %q", s)
		}
	}
}

func TestCodemodeDescriptionListsEverythingWithoutABudget(t *testing.T) {
	all, namespaces, _ := catalogFixture()
	description := codemode.CreateDescription(all, codemode.DescriptionOptions{Namespaces: namespaces})
	mustContain(t, description, "Nested tools:", "## mcp__github\nGitHub server", "## mcp__docs\n\n### `mcp__docs",
		// The search guidance is always there, so tools that appear later do not change it.
		"To find one, call `await searchTools(query)`")
	mustNotContain(t, description, "COMPLETE list", "PARTIAL", "tools)")
}

func TestCodemodeDescriptionFillsTheBudgetRoundRobinCheapestFirstAndSaysWhatIsMissing(t *testing.T) {
	all, namespaces, _ := catalogFixture()
	budget := 170 // Each small section costs about 42 tokens: one tool per group, then one more.
	description := codemode.CreateDescription(all, codemode.DescriptionOptions{Namespaces: namespaces, InlineBudget: &budget})
	mustContain(t, description, "### `read_notes`", "## mcp__docs (some tools not listed)", "### `mcp__docs__search`", "## mcp__github (some tools not listed)", "To find one, call `await searchTools(query)`")
	mustNotContain(t, description, "### `mcp__docs__long`")
	if again := codemode.CreateDescription(all, codemode.DescriptionOptions{Namespaces: namespaces, InlineBudget: &budget}); again != description {
		t.Error("the same input gave a different description")
	}
}

func TestCodemodeDescriptionLeavesDeferredToolsAndTheirNamespacesOutEntirely(t *testing.T) {
	all, namespaces, github := catalogFixture()
	deferred := map[string]bool{}
	for _, tool := range github {
		deferred[tool.Name] = true
	}
	description := codemode.CreateDescription(all, codemode.DescriptionOptions{Namespaces: namespaces, Deferred: deferred})
	mustNotContain(t, description, "mcp__github")
	// Deferred tools, such as those of a server that connects later, do not change the description.
	without := append([]extension.AgentTool{all[0]}, all[len(all)-2:]...)
	if want := codemode.CreateDescription(without, codemode.DescriptionOptions{Namespaces: namespaces}); description != want {
		t.Errorf("a description with deferred tools differs from one without them:\n%s\n---\n%s", description, want)
	}
}

func TestCodemodeDescriptionLeavesNamespaceInstructionsOut(t *testing.T) {
	_, _, github := catalogFixture()
	namespaces := map[string]extension.ToolNamespace{}
	for _, tool := range github {
		namespaces[tool.Name] = extension.ToolNamespace{Name: "mcp__github", Instructions: "Long usage guide."}
	}
	description := codemode.CreateDescription(github, codemode.DescriptionOptions{Namespaces: namespaces})
	mustContain(t, description, "## mcp__github\n\n### `mcp__github__a`")
	mustNotContain(t, description, "Long usage guide.")
}

func TestCodemodeDescriptionListsOnlyNamespacesWithAZeroBudget(t *testing.T) {
	all, namespaces, _ := catalogFixture()
	zero := 0
	description := codemode.CreateDescription(all, codemode.DescriptionOptions{Namespaces: namespaces, InlineBudget: &zero})
	mustContain(t, description, "## mcp__docs (tools not listed)")
	mustNotContain(t, description, "codemode tool declaration:")
}
