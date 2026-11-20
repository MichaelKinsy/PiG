//go:build !pig_strip_mcp

package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/mcpchannel"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestMCPChannelRendererUsesHostAttribution(t *testing.T) {
	runner := mcpRendererRunner(t, extension.Extension{MessageRenderers: map[string]extension.MessageRenderer{
		mcpchannel.CustomType: func(extension.CustomMessage, extension.MessageRenderOptions, extension.Theme) extension.Component {
			return tui.NewText("ordinary extension renderer")
		},
	}})
	custom := map[string]any{
		"customType": mcpchannel.CustomType, "content": "build done", "display": true,
		"details": map[string]any{"source": "not the host source"},
	}
	ordinary, _ := customMessageEntryOf(custom)
	mcpchannel.ValidatedSource("ci").Attribute(custom)
	live, _ := customMessageEntryOf(custom)
	wire, err := json.Marshal(live)
	if err != nil {
		t.Fatal(err)
	}
	var restored CustomMessageEntry
	if err := json.Unmarshal(wire, &restored); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want, reject string
		entry              CustomMessageEntry
	}{
		{"ordinary", "ordinary extension renderer", "←", ordinary},
		{"live", "← ci", "ordinary extension renderer", live},
		{"restored", "← ci", "ordinary extension renderer", restored},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &InteractiveMode{newRunner: runner, chatContainer: tui.NewContainer()}
			m.appendCustomMessage(tc.entry)
			got := stripANSITest(strings.Join(m.chatContainer.Render(80), "\n"))
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.reject) || strings.Contains(got, "not the host source") {
				t.Fatalf("transcript = %q; want %q without %q or details attribution", got, tc.want, tc.reject)
			}
			t.Logf("transcript: %s", strings.TrimSpace(got))
		})
	}
}
