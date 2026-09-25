package subprocess

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func BenchmarkPromptHandlerFIFOThroughFusedSDK(b *testing.B) {
	ext := sdk.New("prompt-bench")
	entered := make(chan struct{}, 2)
	for _, event := range []string{"ui_prompt_start", "ui_prompt_end"} {
		ext.OnEvent(event, func(sdk.Context, map[string]any) (any, error) { entered <- struct{}{}; return nil, nil })
	}
	host := NewHost(b.TempDir())
	defer host.Shutdown("benchmark complete")
	loaded, err := host.LoadInProcess(b.Context(), ExtConfig{Name: "prompt-bench", Enabled: true}, ext.RunWithConn)
	if err != nil {
		b.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{*loaded}, b.TempDir())
	defer runner.Invalidate("benchmark complete")
	b.ReportAllocs()
	for b.Loop() {
		runner.BeginUIPrompt(extension.UIPromptKindInput, "prompt")()
		<-entered
		<-entered
	}
}
