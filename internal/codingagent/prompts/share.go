// SPDX-License-Identifier: MIT

package prompts

import (
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

// RenderSharingSections returns the text ai.GetCurrentSystemPrompt renders for sections (the non-empty section values joined by a blank line) and
// points each of those values at its own place in that text, so the prompt is held once: a project's context files can make it tens of kilobytes,
// and the sections and the text are otherwise two copies of it. The value of every section reads the same bytes as before. The caller owns sections:
// each value is replaced through its pointer, so sections built for this call (BuildSystemPromptSections) are the ones to pass.
func RenderSharingSections(sections ai.OrderedSections) string {
	size, count := 0, 0
	for _, section := range sections {
		if section.Value != nil && *section.Value != "" {
			size += len(*section.Value)
			count++
		}
	}
	if count == 0 {
		return ""
	}
	size += 2 * (count - 1)
	var text strings.Builder
	text.Grow(size)
	for _, section := range sections {
		if section.Value != nil && *section.Value != "" {
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(*section.Value)
		}
	}
	rendered := text.String()
	offset := 0
	for _, section := range sections {
		if section.Value != nil && *section.Value != "" {
			end := offset + len(*section.Value)
			*section.Value = rendered[offset:end]
			offset = end + 2
		}
	}
	return rendered
}
