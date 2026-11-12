package coding

import (
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	codingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// runSystemPromptMemo is the text of the run's system prompt with every input it was built from. A run's prompt is a function of those inputs alone, and the extension host reads it before each event it dispatches, so a prompt whose inputs are unchanged is not built again.
//
// The inputs are compared as the code that changes them publishes them: the run and the hidden declarations are replaced through atomic pointers and never written after publication (a write would race with the readers that hold no lock); the kind of prompt (structured or opaque, built whole from the run's options or the caller's own), the active tool names and the base sections, which the Session changes under the write lock of toolRegistryMu, are compared by value. The base options are not an input: the run holds its own copy of them (agent-session.ts:2005, runner.ts:1438), and an edit of the base reaches the next run.
type runSystemPromptMemo struct {
	mu         sync.Mutex
	set        bool
	run        *codingagent.BeforeAgentStartRun
	structured bool
	whole      bool
	hidden     *map[string]struct{}
	active     []string
	sections   ai.OrderedSections
	text       string
}

// runSystemPromptLocked returns the system prompt text of run, built from the Session's tools, hidden declarations and base sections as effectiveSystemSectionsFor reads them. The caller holds toolRegistryMu.
func (s *Session) runSystemPromptLocked(run *codingagent.BeforeAgentStartRun) (string, error) {
	structured, whole := s.structuredSystemPrompt, s.buildsRunPromptWhole()
	hidden := s.loadout.hidden.Load()
	active := s.ActiveToolNames()
	memo := &s.runSystemPromptMemo
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if memo.set && memo.run == run && memo.structured == structured && memo.whole == whole && memo.hidden == hidden && slices.Equal(memo.active, active) && equalSections(memo.sections, s.baseSystemSections) {
		return memo.text, nil
	}
	sections, err := s.effectiveSystemSectionsFor(run)
	if err != nil {
		return "", err
	}
	text := ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: sections}})
	memo.set, memo.run, memo.structured, memo.whole, memo.hidden, memo.active = true, run, structured, whole, hidden, active
	memo.sections, memo.text = cloneSystemSections(s.baseSystemSections), text
	return text, nil
}

// equalSections reports whether a and b hold the same sections: the same names and values, in order.
func equalSections(a, b ai.OrderedSections) bool {
	return slices.EqualFunc(a, b, func(x, y ai.PromptSection) bool {
		return x.Name == y.Name && (x.Value == nil) == (y.Value == nil) && (x.Value == nil || *x.Value == *y.Value)
	})
}
