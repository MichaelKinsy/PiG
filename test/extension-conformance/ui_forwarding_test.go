package extensionconformance

import (
	"context"
	"reflect"
	"testing"
)

// TestConformance_UIForwardingAgreesAcrossSDKs: setWorkingMessage, setHiddenThinkingLabel, setWorkingVisible, setWorkingIndicator, pasteToEditor, setEditorText and setToolsExpanded
// (types.ts ExtensionUIContext) reach the host's UI with the same arguments from every SDK, in call order. A non-BMP character in the pasted text survives
// the wire in each language; the indicator's frames and interval keep their types. An empty working message and an absent one stay distinct (Pi's message ?? default).
func TestConformance_UIForwardingAgreesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	want := []string{
		"workingMessage:Thinking hard",
		"workingMessage:",          // an empty string is a message: Pi shows an empty label
		"workingMessage:<default>", // no argument restores the default label (message ?? default)
		"hiddenThinkingLabel:Pondering",
		"hiddenThinkingLabel:",          // an empty string is a label: Pi shows an empty one
		"hiddenThinkingLabel:<default>", // no argument restores the default label (label ?? default)
		"workingVisible:false",
		`workingIndicator:{"frames":["-","+"],"intervalMs":120}`,
		"paste:pasted \U0001F600",
		"editorText:draft",
		"toolsExpanded:true",
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
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
			if !h.runner.ExecuteCommand(context.Background(), "ui_forward_probe", "") {
				t.Fatal("fixture has no ui_forward_probe command")
			}
			var got []string
			pollUntilConformance(t, "ui_forward_probe: calls never arrived", func() bool {
				got = h.ui.RecordedCalls()
				return len(got) >= len(want)
			})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("host UI calls = %q, want %q", got, want)
			}
		})
	}
}
