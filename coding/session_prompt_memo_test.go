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

	// The base sections, edited in place under the write lock as rebuildSystemPrompt does, and the base options, for a prompt whose sections are the caller's.
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
	// The active tool names are the one allocation of the key.
	if allocs > 3 {
		t.Fatalf("an unchanged run prompt costs %.0f allocations, want the key's names only", allocs)
	}
}

// The key the extension host reads names the base options the Session hands out: it changes with every replacement of them (the tools, the resources), the options it names are the ones GetSystemPromptOptions returns, and it holds while they do not change.
func TestSystemPromptOptionsKeyTracksTheBaseOptions(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true})
	s := h.session
	key := func() systemPromptOptionsKey {
		return systemPromptOptionsKey{session: s, generation: s.systemPromptOptionsGen.Load()}
	}
	options := s.GetSystemPromptOptions()
	first := key()
	if again := s.GetSystemPromptOptions(); again != options || key() != first {
		t.Fatal("unchanged inputs changed the options object or its key")
	}
	active := s.ActiveToolNames()
	if len(active) < 2 {
		t.Fatalf("active tools %v: the test needs two", active)
	}

	s.SetActiveToolsByName(active[:1])
	if key() == first || s.GetSystemPromptOptions() == options || len(s.GetSystemPromptOptions().SelectedTools) != 1 {
		t.Fatalf("a tool change left the options %+v under key %v", s.GetSystemPromptOptions().SelectedTools, key())
	}
	afterTools, toolsKey := s.GetSystemPromptOptions(), key()

	s.SetSystemPromptResources(SystemPromptResources{AppendSystemPrompt: "APPENDED-BY-RESOURCES"})
	if key() == toolsKey || s.GetSystemPromptOptions() == afterTools || s.GetSystemPromptOptions().AppendSystemPrompt != "APPENDED-BY-RESOURCES" {
		t.Fatalf("a resource change left the options %+v under key %v", s.GetSystemPromptOptions(), key())
	}
}
