package prompts

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func BenchmarkToolPromptContributions(b *testing.B) {
	for _, count := range []int{8, 128, 1024} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			definitions := make([]extension.RegisteredTool, count)
			names := make([]string, count)
			for i := range definitions {
				names[i] = fmt.Sprintf("extension_tool_%d", i)
				definitions[i].Definition = extension.ToolDefinition{Name: names[i], PromptSnippet: " Find\r\n project symbols ", PromptGuidelines: []string{" shared guidance ", names[i] + " has scoped permissions"}}
			}
			options := Options{Cwd: "/project", Tools: names, ToolHints: DefaultToolSnippets()}
			b.ReportAllocs()
			for b.Loop() {
				_ = BuildSystemPromptSections(WithToolDefinitions(options, definitions))
			}
		})
	}
}
