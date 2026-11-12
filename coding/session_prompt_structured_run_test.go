package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A caller that supplies its structured prompt together with the resources behind it, as interactive mode does, gets the run's prompt built whole from the run's options, as Pi builds every run's (agent-session.ts:1828-1840, 2043). A loadout without the read tool changes the skills section as well as the tools and rules (system-prompt.ts:177), so the caller's sections are not the rendering of the options any more; the handlers' edits and the new reader reach the prompt all the same.
func TestStructuredPromptWithResourcesIsBuiltFromTheRunOptionsAfterALoadoutChange(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true, resources: &SystemPromptResources{
		AppendSystemPrompt: "BASE-APPENDED",
		Skills:             []extension.SystemPromptSkill{{Name: "demo", Description: "DEMO-SKILL", FilePath: "/skills/demo/SKILL.md"}},
	}})
	s := h.session
	s.SetSystemPromptSections(icodingagent.BeforeAgentStartRun{Options: *s.GetSystemPromptOptions()}.BaseSections(s.ActiveToolNames(), nil))
	prompt := func() string {
		t.Helper()
		base := *s.GetSystemPromptOptions()
		edited := base
		edited.AppendSystemPrompt = "RUN-APPENDED"
		run, err := icodingagent.ResolveBeforeAgentStartRun(base, &extension.BeforeAgentStartCombinedResult{SystemPromptOptions: &edited})
		if err != nil {
			t.Fatal(err)
		}
		s.toolRegistryMu.RLock()
		defer s.toolRegistryMu.RUnlock()
		sections, err := s.effectiveSystemSectionsFor(&run)
		if err != nil {
			t.Fatal(err)
		}
		return ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: sections}})
	}
	if got := prompt(); !strings.Contains(got, "RUN-APPENDED") || strings.Contains(got, "BASE-APPENDED") || !strings.Contains(got, "Use the read tool to load a skill's file") {
		t.Fatalf("the run's prompt with the starting loadout:\n%s", got)
	}
	s.SetActiveToolsByName([]string{"bash"})
	if got := prompt(); !strings.Contains(got, "RUN-APPENDED") || strings.Contains(got, "BASE-APPENDED") || !strings.Contains(got, "Use bash to load a skill's file") {
		t.Fatalf("the run's prompt after the loadout lost the read tool:\n%s", got)
	}
}
