package extensionconformance

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// This measures the real host/SDK codec and awaited mutation response, not provider latency or Session persistence.
func BenchmarkPromptSectionMutationFused(b *testing.B) {
	for _, count := range []int{1, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			host := subprocess.NewHostWithConfigRoot(b.TempDir(), b.TempDir())
			b.Cleanup(func() { host.Shutdown("benchmark complete") })
			ext := sdk.New("sections")
			ext.OnEvent("before_agent_start", func(_ sdk.Context, data map[string]any) (any, error) {
				data["systemPromptOptions"].(map[string]any)["sections"].(*sdk.SystemPromptSections).Set("plan_mode", "Plan only.")
				return nil, nil
			})
			loaded, err := host.LoadInProcess(b.Context(), subprocess.ExtConfig{Name: "sections", Enabled: true}, ext.RunWithConn)
			if err != nil {
				b.Fatal(err)
			}
			runner := inproc.NewRunner([]extension.Extension{*loaded}, b.TempDir())
			b.Cleanup(func() { runner.Invalidate("benchmark complete") })
			base := make(ai.OrderedSections, count)
			for i := range base {
				base[i] = ai.PromptSection{Name: fmt.Sprintf("section_%d", i), Value: new("retained instructions")}
			}
			b.ReportAllocs()
			for b.Loop() {
				sections := append(ai.OrderedSections(nil), base...)
				result, err := runner.EmitBeforeAgentStart(b.Context(), "hello", nil, "base", extension.BuildSystemPromptOptions{Sections: &sections})
				if err != nil || result == nil || result.SystemPromptOptions == nil || result.SystemPromptOptions.Sections == nil || len(*result.SystemPromptOptions.Sections) != len(base)+1 {
					b.Fatalf("result=%+v err=%v", result, err)
				}
			}
		})
	}
}
