package extensionconformance

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// runner.ts queues event microtasks in FIFO order. Record a sequence number
// inside the handler before any host call; notification completion may reorder,
// but the sequence-to-event association may not.
func TestPromptHandlerBodyFIFOAcrossSDKs(t *testing.T) {
	const prompts = 512
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				h.runner.Invalidate("test complete")
				if h.cleanup != nil {
					h.cleanup()
				}
				h.host.Shutdown("test complete")
			})
			for i := range prompts {
				h.runner.BeginUIPrompt(extension.UIPromptKindInput, fmt.Sprintf("fifo:%d", i))()
			}
			waitFor(t, func() bool { return len(h.ui.Recorded()) >= 2*prompts })
			records := h.ui.Recorded()
			if len(records) != 2*prompts {
				t.Fatalf("handler entries = %d, want %d", len(records), 2*prompts)
			}
			seen := make(map[int]bool)
			for _, record := range records {
				parts := strings.SplitN(record, ":", 3)
				if len(parts) != 3 || parts[0] != "fifo" {
					t.Fatalf("invalid entry recording %q", record)
				}
				sequence, err := strconv.Atoi(parts[1])
				if err != nil || sequence < 0 || sequence >= 2*prompts || seen[sequence] {
					t.Fatalf("invalid/duplicate handler sequence %q", record)
				}
				seen[sequence] = true
				kind := "ui_prompt_start"
				if sequence%2 == 1 {
					kind = "ui_prompt_end"
				}
				want := fmt.Sprintf("%s:fifo:%d:info", kind, sequence/2)
				if parts[2] != want {
					t.Fatalf("handler body %d = %s, want %s", sequence, parts[2], want)
				}
			}
		})
	}
}
