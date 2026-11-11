package coding

import (
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	codingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// runSystemPromptMemo is the text of the run's system prompt with every input it was built from. A run's prompt is a function of those inputs alone, and the extension host reads it before each event it dispatches, so a prompt whose inputs are unchanged is not built again.
//
// The inputs are compared as the code that changes them publishes them: the run, the base options and the hidden declarations are replaced through atomic pointers and never written after publication (a write would race with the readers that hold no lock), and the active tool names and the base sections, which loadout changes edit in place under the write lock of toolRegistryMu, are compared by value.
type runSystemPromptMemo struct {
	mu       sync.Mutex
	set      bool
	run      *codingagent.BeforeAgentStartRun
	base     *extension.BuildSystemPromptOptions
	hidden   *map[string]struct{}
	active   []string
	sections ai.OrderedSections
	text     string
}

// runSystemPromptLocked returns the system prompt text of run, built from the Session's tools, hidden declarations and base sections as effectiveSystemSectionsFor reads them. The caller holds toolRegistryMu.
func (s *Session) runSystemPromptLocked(run *codingagent.BeforeAgentStartRun) (string, error) {
	base := s.baseSystemPromptOptions.Load()
	hidden := s.loadout.hidden.Load()
	active := s.ActiveToolNames()
	memo := &s.runSystemPromptMemo
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if memo.set && memo.run == run && memo.base == base && memo.hidden == hidden && slices.Equal(memo.active, active) && equalSections(memo.sections, s.baseSystemSections) {
		return memo.text, nil
	}
	sections, err := s.effectiveSystemSectionsFor(run)
	if err != nil {
		return "", err
	}
	text := ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: sections}})
	memo.set, memo.run, memo.base, memo.hidden, memo.active = true, run, base, hidden, active
	memo.sections, memo.text = cloneSystemSections(s.baseSystemSections), text
	return text, nil
}

// equalSections reports whether a and b hold the same sections: the same names and values, in order.
func equalSections(a, b ai.OrderedSections) bool {
	return slices.EqualFunc(a, b, func(x, y ai.PromptSection) bool {
		return x.Name == y.Name && (x.Value == nil) == (y.Value == nil) && (x.Value == nil || *x.Value == *y.Value)
	})
}
