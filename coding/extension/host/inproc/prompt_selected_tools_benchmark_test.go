package inproc

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func BenchmarkBeforeAgentStartSelectedTools(b *testing.B) {
	for _, count := range []int{0, 8, 128, 1024} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			options := extension.BuildSystemPromptOptions{SelectedTools: make([]string, count)}
			for i := range options.SelectedTools {
				options.SelectedTools[i] = fmt.Sprintf("tool-%d", i)
			}
			runner := NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
				options := extension.BeforeAgentStartOptions(args[1].(context.Context))
				options.SelectedTools = append(options.SelectedTools, "extra")
				return nil, nil
			}}}}}, b.TempDir())
			b.Cleanup(func() { runner.Invalidate("benchmark complete") })
			b.ReportAllocs()
			for b.Loop() {
				if _, err := runner.EmitBeforeAgentStart(b.Context(), "prompt", nil, "base", options); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
