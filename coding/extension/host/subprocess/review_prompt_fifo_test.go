package subprocess

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// runner.ts queues emission microtasks: the first synchronous handler body
// runs in admission order, independently of asynchronous completion.
func TestReviewPromptHandlerBodyFIFOThroughFusedSDK(t *testing.T) {
	const prompts = 512
	seen := make(chan string, 2*prompts)
	ext := sdk.New("fifo-review")
	for _, event := range []string{"ui_prompt_start", "ui_prompt_end"} {
		ext.OnEvent(event, func(_ sdk.Context, data map[string]any) (any, error) {
			seen <- fmt.Sprintf("%s:%s", data["type"], data["title"])
			return nil, nil
		})
	}
	host := NewHost(t.TempDir())
	defer host.Shutdown("test complete")
	loaded, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "fifo-review", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{*loaded}, t.TempDir())
	defer runner.Invalidate("test complete")
	for i := range prompts {
		runner.BeginUIPrompt(extension.UIPromptKindInput, fmt.Sprint(i))()
	}
	var firstMismatch string
	for i := range 2 * prompts {
		got := <-seen
		kind := "ui_prompt_start"
		if i%2 == 1 {
			kind = "ui_prompt_end"
		}
		want := fmt.Sprintf("%s:%d", kind, i/2)
		if got != want && firstMismatch == "" {
			firstMismatch = fmt.Sprintf("handler body %d = %s, want %s", i, got, want)
		}
	}
	if firstMismatch != "" {
		t.Fatal(firstMismatch)
	}
}
