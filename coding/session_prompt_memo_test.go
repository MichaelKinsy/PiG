package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The prompt the extension host reads before each event is the run's prompt as Session builds it now: every input the memo keys on changes it, and one of them restored gives the earlier prompt back.
func TestRunSystemPromptFollowsEveryInput(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true})
	s := h.session
	options := *s.GetSystemPromptOptions()
	run, err := icodingagent.ResolveBeforeAgentStartRun(options, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.SetRunPrompt(&run)
	first := s.SystemPrompt()
	if first == "" || first != s.SystemPrompt() {
		t.Fatalf("the run's prompt is %q then %q", first, s.SystemPrompt())
	}

	changed := func(what, got string) {
		t.Helper()
		if got == first {
			t.Fatalf("%s did not change the system prompt", what)
		}
	}

	// The run.
	section := ai.OrderedSections{{Name: "mcp_servers", Value: new("- mcp__docs (codemode)")}}
	withSection := options
	withSection.Sections = &section
	other, err := icodingagent.ResolveBeforeAgentStartRun(options, &extension.BeforeAgentStartCombinedResult{SystemPromptOptions: &withSection})
	if err != nil {
		t.Fatal(err)
	}
	s.SetRunPrompt(&other)
	got := s.SystemPrompt()
	changed("another run", got)
	if !strings.Contains(got, "mcp__docs") {
		t.Fatalf("the prompt of the run with a section lacks it: %q", got)
	}
	s.SetRunPrompt(&run)
	if got := s.SystemPrompt(); got != first {
		t.Fatalf("the first run's prompt came back as %q, want %q", got, first)
	}

	// The active tools.
	active := s.ActiveToolNames()
	if len(active) < 2 {
		t.Fatalf("active tools %v: the test needs two", active)
	}
	s.SetActiveToolsByName(active[:1])
	changed("deactivating a tool", s.SystemPrompt())
	s.SetActiveToolsByName(active)
	if got := s.SystemPrompt(); got != first {
		t.Fatalf("the prompt with the tools restored is %q, want %q", got, first)
	}

	// The hidden declarations, replaced through their pointer as applyToolLoadout does.
	s.toolRegistryMu.Lock()
	s.loadout.hidden.Store(&map[string]struct{}{active[0]: {}})
	s.toolRegistryMu.Unlock()
	changed("hiding a declaration", s.SystemPrompt())
	s.toolRegistryMu.Lock()
	s.loadout.hidden.Store(nil)
	s.toolRegistryMu.Unlock()
	if got := s.SystemPrompt(); got != first {
		t.Fatalf("the prompt with nothing hidden is %q, want %q", got, first)
	}

	// The base sections, edited in place under the write lock as rebuildSystemPrompt does, for a prompt whose sections are the caller's.
	s.toolRegistryMu.Lock()
	s.defaultSystemPrompt = false
	s.toolRegistryMu.Unlock()
	callers := s.SystemPrompt()
	s.toolRegistryMu.Lock()
	index := -1
	for i, section := range s.baseSystemSections {
		if section.Name != "tools" && section.Name != "rules" && section.Value != nil {
			index = i
			break
		}
	}
	if index < 0 {
		s.toolRegistryMu.Unlock()
		t.Fatalf("no caller section in %+v", s.baseSystemSections)
	}
	s.baseSystemSections[index] = ai.PromptSection{Name: s.baseSystemSections[index].Name, Value: new("EDITED-IN-PLACE")}
	s.toolRegistryMu.Unlock()
	if got := s.SystemPrompt(); got == callers || !strings.Contains(got, "EDITED-IN-PLACE") {
		t.Fatalf("an edited base section did not reach the prompt: %q", got)
	}
}

// The run's prompt follows the kind of prompt the Session has. A caller-owned structured prompt takes only the tools and rules from the run's options; once the caller supplies the resources behind it, the same run's prompt is built whole from the run's options, as effectiveSystemSections builds the request's.
func TestRunSystemPromptFollowsTheResourcesBehindACallerPrompt(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true})
	s := h.session
	s.SetSystemPromptSections(ai.OrderedSections{{Name: "preamble", Value: new("CALLER-PREAMBLE")}})
	base := *s.GetSystemPromptOptions()
	edited := base
	edited.AppendSystemPrompt = "RUN-APPENDED"
	run, err := icodingagent.ResolveBeforeAgentStartRun(base, &extension.BeforeAgentStartCombinedResult{SystemPromptOptions: &edited})
	if err != nil {
		t.Fatal(err)
	}
	s.SetRunPrompt(&run)
	if got := s.SystemPrompt(); !strings.Contains(got, "CALLER-PREAMBLE") || strings.Contains(got, "RUN-APPENDED") {
		t.Fatalf("the run's prompt over a caller-owned prompt:\n%s", got)
	}
	s.SetSystemPromptResources(SystemPromptResources{})
	requested := func() string {
		t.Helper()
		s.toolRegistryMu.RLock()
		defer s.toolRegistryMu.RUnlock()
		sections, err := s.effectiveSystemSections()
		if err != nil {
			t.Fatal(err)
		}
		return ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: sections}})
	}
	if got, want := s.SystemPrompt(), requested(); got != want || !strings.Contains(got, "RUN-APPENDED") {
		t.Fatalf("the run's prompt once the caller supplied resources:\n%s\nthe request's:\n%s", got, want)
	}
}

// A prompt whose inputs did not change is not built again: the host reads it before every event it dispatches.
func TestRunSystemPromptIsNotRebuiltForUnchangedInputs(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true})
	s := h.session
	run, err := icodingagent.ResolveBeforeAgentStartRun(*s.GetSystemPromptOptions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.SetRunPrompt(&run)
	prompt := s.SystemPrompt()
	var got string
	allocs := testing.AllocsPerRun(50, func() { got = s.SystemPrompt() })
	if got != prompt {
		t.Fatalf("the prompt changed from %q to %q", prompt, got)
	}
	t.Logf("an unchanged run prompt costs %.0f allocations", allocs)
	// The active tool names are the one allocation of the key.
	if allocs > 3 {
		t.Fatalf("an unchanged run prompt costs %.0f allocations, want the key's names only", allocs)
	}
}
