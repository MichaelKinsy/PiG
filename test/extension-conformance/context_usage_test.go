package extensionconformance

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi agent-session.ts:3858-3900: missing model/window gives undefined; post-compaction usage is null, not zero.
func TestContextUsagePresenceAcrossSDKs(t *testing.T) {
	t.Parallel()
	cases := allHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var usage *extension.ContextUsage
			getUsage := func() *extension.ContextUsage { return usage }
			h.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetContextUsage: getUsage}, nil)
			if h.bridge != nil {
				h.bridge.SetHostAction("getContextUsage", getUsage)
			}
			zero, measured := 0, 15
			zeroPercent, percent := 0.0, 0.01171875
			for _, value := range []*extension.ContextUsage{
				nil,
				{Tokens: &measured, ContextWindow: 128000, Percent: &percent},
				{ContextWindow: 128000},
				{Tokens: &zero, ContextWindow: 128000, Percent: &zeroPercent},
				nil,
			} {
				usage = value
				h.ui.ClearRecorded()
				command, ok := findCommand(h.runner, "usage-probe")
				if !ok {
					t.Fatal("usage-probe not registered")
				}
				cc := h.runner.CreateCommandContext()
				ctx := extension.WithCommandContext(extension.WithContext(t.Context(), cc.Context), cc)
				if err := command.Handler(ctx, ""); err != nil {
					t.Fatal(err)
				}
				recorded := h.ui.Recorded()
				if len(recorded) != 1 {
					t.Fatalf("usage notifications: %v", recorded)
				}
				var got, want any
				if err := json.Unmarshal([]byte(strings.TrimSuffix(recorded[0], ":info")), &got); err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(encoded, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("usage = %v, want %v", got, want)
				}
			}
		})
	}
}
