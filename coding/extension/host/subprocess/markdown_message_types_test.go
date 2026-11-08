package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: runner.ts getMarkdownTransformers — a register frame without markdown_message_types leaves the default surfaces (nil), a listed set carries as
// given, and an empty list is a non-nil empty set that runs the transformer nowhere.
func TestRegisteredMarkdownMessageTypesReachTheBuiltExtension(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame string
		want  []extension.MarkdownMessageType
		isNil bool
	}{
		{"absent", `{"name":"md","markdown_transformer":true}`, nil, true},
		{"listed", `{"name":"md","markdown_transformer":true,"markdown_message_types":["custom","changelog"]}`, []extension.MarkdownMessageType{"custom", "changelog"}, false},
		{"empty", `{"name":"md","markdown_transformer":true,"markdown_message_types":[]}`, []extension.MarkdownMessageType{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reg RegisterPayload
			if err := json.Unmarshal([]byte(tc.frame), &reg); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			ext := h.buildExtension(&managedExt{config: ExtConfig{Name: "md"}, host: h}, &reg)
			if ext.MarkdownTransformer == nil {
				t.Fatal("no transformer")
			}
			if (ext.MarkdownMessageTypes == nil) != tc.isNil || len(ext.MarkdownMessageTypes) != len(tc.want) {
				t.Fatalf("types = %#v, want %#v", ext.MarkdownMessageTypes, tc.want)
			}
			for i, want := range tc.want {
				if ext.MarkdownMessageTypes[i] != want {
					t.Fatalf("types = %#v, want %#v", ext.MarkdownMessageTypes, tc.want)
				}
			}
		})
	}
}
