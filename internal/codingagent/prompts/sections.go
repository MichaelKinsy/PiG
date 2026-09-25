package prompts

import "github.com/MichaelKinsy/PiG/ai"

// DiffSystemPromptSections returns replacements in current order, followed by
// removals in previous order. A nil value removes a section from the transcript.
func DiffSystemPromptSections(previous, current ai.OrderedSections) ai.OrderedSections {
	old := make(map[string]*string, len(previous))
	for _, section := range previous {
		old[section.Name] = section.Value
	}
	present := make(map[string]struct{}, len(current))
	var patch ai.OrderedSections
	for _, section := range current {
		present[section.Name] = struct{}{}
		value, ok := old[section.Name]
		if !ok || value == nil || section.Value == nil || *value != *section.Value {
			copy := section
			if section.Value != nil {
				copy.Value = new(*section.Value)
			}
			patch = append(patch, copy)
		}
	}
	for _, section := range previous {
		if _, ok := present[section.Name]; !ok {
			patch = append(patch, ai.PromptSection{Name: section.Name})
		}
	}
	return patch
}

// DefaultToolSnippets are the built-in tool summaries used in the system prompt.
func DefaultToolSnippets() map[string]string {
	return map[string]string{
		"read":       "Read file contents",
		"bash":       "Execute bash commands (ls, grep, find, etc.)",
		"powershell": "Execute PowerShell commands",
		"edit":       "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
		"write":      "Create or overwrite files",
		"grep":       "Search file contents for patterns (respects .gitignore)",
		"find":       "Find files by glob pattern (respects .gitignore)",
		"ls":         "List directory contents",
	}
}
