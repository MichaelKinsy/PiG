package prompts

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// ApplyCustomSystemPromptSections overlays extension-authored sections on the base prompt in insertion order. Empty values leave the base unchanged; preamble and invalid tag names are rejected.
// Ports packages/coding-agent/src/core/system-prompt.ts:121-186.
func ApplyCustomSystemPromptSections(base, custom ai.OrderedSections) (ai.OrderedSections, error) {
	for _, section := range custom {
		if !validSystemPromptSectionName(section.Name) || section.Name == "preamble" {
			return nil, fmt.Errorf("Invalid system prompt section name: %s", section.Name)
		}
	}
	out := slices.Clone(base)
	for _, section := range custom {
		if section.Value == nil || *section.Value == "" {
			continue
		}
		wrapped := ai.PromptSection{Name: section.Name, Value: new("<" + section.Name + ">\n" + *section.Value + "\n</" + section.Name + ">")}
		if i := slices.IndexFunc(out, func(existing ai.PromptSection) bool { return existing.Name == section.Name }); i >= 0 {
			out[i] = wrapped
		} else {
			out = append(out, wrapped)
		}
	}
	return out, nil
}

func validSystemPromptSectionName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, c := range name[1:] {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
