package extensionconformance

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The custom-message renderer receives the setting as a required number, not a
// missing property or SDK default. Both settings differ from an absent field.
func TestMessageOutputPadAcrossSDKs(t *testing.T) {
	for _, tc := range allHarnessCases() {
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
			renderer := h.runner.MessageRenderer("conformance-message")
			if renderer == nil {
				t.Fatal("missing renderer")
			}
			for _, wire := range []string{`{"expanded":false,"outputPad":1}`, `{"expanded":true,"outputPad":0}`} {
				var options extension.MessageRenderOptions
				if err := json.Unmarshal([]byte(wire), &options); err != nil {
					t.Fatal(err)
				}
				component, ok := renderer(extension.CustomMessageRef{CustomType: "conformance-message", Content: "padding-options", Display: true}, options, nil).(interface{ Render(int) []string })
				if !ok {
					t.Fatal("renderer returned no component")
				}
				var got map[string]any
				waitFor(t, func() bool {
					lines := component.Render(72)
					return len(lines) == 1 && json.Unmarshal([]byte(lines[0]), &got) == nil
				})
				var want map[string]any
				if err := json.Unmarshal([]byte(wire), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("renderer saw %v, want %v", got, want)
				}
			}
		})
	}
}
