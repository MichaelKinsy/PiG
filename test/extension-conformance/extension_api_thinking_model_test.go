package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// packages/coding-agent/src/core/extensions/types.ts:1752-1761 (ExtensionAPI.setModel, getThinkingLevel, setThinkingLevel):
// every SDK reads the level the host reports at the moment of the call and a setModel reaches the host as "<provider>/<id>",
// answering the host's verdict. The sentinel level "xhigh" and the verdict false differ from each SDK's empty fallback.
func TestExtensionAPIThinkingLevelAndSetModelAcrossSDKs(t *testing.T) {
	t.Parallel()
	for _, tc := range allHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test complete")
				}
			})
			if h.bridge == nil {
				t.Skip("the in-process harness has no ExtensionAPI implementer; getThinkingLevel and setModel are SDK calls")
			}
			var mu sync.Mutex
			var models []string
			var levels []string
			level := "xhigh"
			h.bridge.SetHostAction("getThinkingLevel", func() string {
				mu.Lock()
				defer mu.Unlock()
				return level
			})
			h.bridge.SetHostAction("setModel", func(_ context.Context, model string) (bool, error) {
				mu.Lock()
				defer mu.Unlock()
				models = append(models, model)
				return false, nil
			})
			h.bridge.SetHostAction("setThinkingLevel", func(set string) {
				mu.Lock()
				defer mu.Unlock()
				levels = append(levels, set)
			})
			command, ok := findCommand(h.runner, "thinking-model")
			if !ok {
				t.Fatal("thinking-model command missing")
			}
			*h.notify = nil
			if err := command.Handler(h.runner.DispatchContext(t.Context()), ""); err != nil {
				t.Fatal(err)
			}
			if len(*h.notify) != 1 {
				t.Fatalf("notifications = %v", *h.notify)
			}
			if got, want := strings.TrimSuffix((*h.notify)[0], ":info"), `["xhigh",false]`; got != want {
				t.Errorf("getThinkingLevel and setModel = %s, want %s", got, want)
			}
			// setThinkingLevel is fire-and-forget on the wire: the host receives it after the command returns.
			pollUntilConformance(t, 5*time.Second, "setThinkingLevel never reached the host", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(levels) > 0
			})
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"high"}; !slices.Equal(levels, want) {
				t.Errorf("setThinkingLevel host calls = %q, want %q", levels, want)
			}
			if want := []string{"probe/model"}; !slices.Equal(models, want) {
				t.Errorf("setModel host calls = %q, want %q", models, want)
			}
		})
	}
}

// packages/coding-agent/src/core/extensions/types.ts:1742 (ExtensionAPI.getCommands): every SDK lists the slash commands the host reports,
// with their name, source and description. The host's list is not a command any fixture registers, so an SDK that answers from its own
// registrations or with an empty fallback fails.
func TestExtensionAPIGetCommandsAcrossSDKs(t *testing.T) {
	requireAPIMember(t, "GetCommands", extension.API.GetCommands)
	t.Parallel()
	for _, tc := range allHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test complete")
				}
			})
			if h.bridge == nil {
				t.Skip("the in-process harness has no ExtensionAPI implementer; getCommands is an SDK call")
			}
			command, ok := findCommand(h.runner, "commands-probe")
			if !ok {
				t.Fatal("commands-probe command missing")
			}
			*h.notify = nil
			if err := command.Handler(h.runner.DispatchContext(t.Context()), ""); err != nil {
				t.Fatal(err)
			}
			if len(*h.notify) != 1 {
				t.Fatalf("notifications = %v", *h.notify)
			}
			if got, want := strings.TrimSuffix((*h.notify)[0], ":info"), `[["conformance-listed","prompt","Listed by the host"]]`; got != want {
				t.Errorf("getCommands = %s, want %s", got, want)
			}
		})
	}
}
