package tui

import (
	"strings"
	"testing"
)

// Ports the 'adds OSC 133 zone markers once across repeated renders' tests of Pi b7bb7ca905b8 (#9549: container output is reused
// across frames, so markers must not be added to it in place) for the user and assistant message components. Pi's `expect(second).toBe(first)`
// is the container-identity contract and is asserted in container_output_reuse_upstream_test.go; here the observable is that a
// repeated render carries each marker once.

func TestUserMessageAddsOSC133ZoneMarkersOnceAcrossRepeatedRenders(t *testing.T) {
	component := NewUserMessageComponent("hello", nil, 1, nil, "", "")
	component.Render(20)
	second := component.Render(20)
	if n := len(strings.Split(second[0], userMessageZoneStart)); n != 2 {
		t.Errorf("zone start appears %d times in the first row of the second render, want once: %q", n-1, second[0])
	}
	if n := len(strings.Split(second[2], userMessageZoneEnd)); n != 2 {
		t.Errorf("zone end appears %d times in the last row of the second render, want once: %q", n-1, second[2])
	}
}

func TestAssistantMessageAddsOSC133ZoneMarkersOnceAcrossRepeatedRenders(t *testing.T) {
	component := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	component.SetContent([]AssistantSegment{{Text: "hello"}})
	component.FinishStreaming()
	component.Render(40)
	second := component.Render(40)
	if n := len(strings.Split(second[0], assistantZoneStart)); n != 2 {
		t.Errorf("zone start appears %d times in the first row of the second render, want once: %q", n-1, second[0])
	}
	if n := len(strings.Split(second[len(second)-1], assistantZoneEnd)); n != 2 {
		t.Errorf("zone end appears %d times in the last row of the second render, want once: %q", n-1, second[len(second)-1])
	}
}
