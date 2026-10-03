package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// .upstream/v1.0.0/packages/coding-agent/test/suite/regressions/5943-session-start-notify.test.ts:220.
// Pi showLoadedResources clears only the resource container, which precedes chat in the root.
// Pi 1.0.0's fake stubs shouldShowStartupDetails to true (test :212); Verbose makes it true here
// (.upstream/v1.0.0/packages/coding-agent/src/modes/interactive/interactive-mode.ts:1415).
func TestSessionStartNotifyOriginalLoadedResources(t *testing.T) {
	mode := &InteractiveMode{
		opts: InteractiveOptions{CWD: "/repo", Verbose: true, NoThemes: true,
			ContextFiles: []ContextFile{{Path: "/repo/AGENTS.md"}}},
		loadedResourcesContainer: tui.NewContainer(),
		chatContainer:            tui.NewContainer(),
	}
	root := tui.NewContainer()
	root.Add(mode.loadedResourcesContainer)
	root.Add(mode.chatContainer)
	mode.loadedResourcesContainer.Add(tui.NewPaddedText("stale resources", 0, 0, nil))
	mode.chatContainer.Add(tui.NewPaddedText("restored message", 0, 0, nil))

	mode.showLoadedResources(false, false)

	chat := strings.Join(mode.chatContainer.Render(80), "\n")
	rendered := strings.Join(root.Render(80), "\n")
	observations := []bool{
		strings.Contains(chat, "restored message"),
		strings.Contains(chat, "[Context]"),
		strings.Contains(rendered, "stale resources"),
		strings.Contains(rendered, "[Context]"),
		strings.Index(rendered, "[Context]") < strings.Index(rendered, "restored message"),
	}
	if !observations[0] || observations[1] || observations[2] || !observations[3] || !observations[4] {
		t.Fatalf("resource/chat observations = %v\nchat: %q\nroot: %q", observations, chat, rendered)
	}
	if os.Getenv("PIG_RUNTIME_ORIGINAL_PROBE") != "" {
		data, err := json.Marshal([]any{"notify", 220, observations})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("RUNTIME_OBSERVATION " + string(data))
	}
}
