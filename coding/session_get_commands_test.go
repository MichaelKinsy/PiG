package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:3361-3384 _bindExtensionCore getCommands: the runner's extension commands, then the prompt
// templates, then the skills (named "skill:<name>"), each carrying the source info its loader recorded.
func TestBindExtensionCoreBindsGetCommandsInPiOrder(t *testing.T) {
	h := newQueueCharacterizationHarness(t, extension.Extension{Commands: map[string]extension.RegisteredCommand{
		"hello": {Name: "hello", Description: "Say hello"},
	}}, nil)
	templateSource := extension.SourceInfo{Path: "/p/review.md", Source: "local", Scope: "user", Origin: "top-level"}
	skillSource := extension.SourceInfo{Path: "/s/lint/SKILL.md", Source: "local", Scope: "project", Origin: "top-level"}
	h.session.SetPromptResources(
		[]PromptTemplate{{Name: "review", Description: "Review code", FilePath: "/p/review.md", SourceInfo: templateSource}},
		[]*Skill{{Name: "lint", Description: "Lint it", FilePath: "/s/lint/SKILL.md", SourceInfo: skillSource}},
	)
	runner := h.session.currentRunner()
	h.session.bindExtensionCore(runner)

	got := runner.Runtime().GetCommands()
	names := make([]string, len(got))
	sources := make([]string, len(got))
	for i, command := range got {
		names[i], sources[i] = command.Name, command.Source
	}
	if want := []string{"hello", "review", "skill:lint"}; !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if want := []string{"extension", "prompt", "skill"}; !slices.Equal(sources, want) {
		t.Fatalf("sources = %v, want %v", sources, want)
	}
	if got[0].Description != "Say hello" || got[1].Description != "Review code" || got[2].Description != "Lint it" {
		t.Fatalf("descriptions = %q %q %q", got[0].Description, got[1].Description, got[2].Description)
	}
	if got[1].SourceInfo != templateSource || got[2].SourceInfo != skillSource {
		t.Fatalf("source infos = %+v %+v, want the loader's", got[1].SourceInfo, got[2].SourceInfo)
	}
}
