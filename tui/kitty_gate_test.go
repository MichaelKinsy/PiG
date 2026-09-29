package tui

import (
	"slices"
	"testing"
)

// Pi tui-main-screen.ts collects and deletes IDs from emitted lines, even when a custom component emits Kitty data without advertised capability support.
func TestKittyImageScansFollowEmittedContent(t *testing.T) {
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	const kittyLine = "\x1b_Gi=42,a=T;payload\x1b\\"
	for _, protocol := range []ImageProtocol{ImageProtocolKitty, ImageProtocolITerm2, ""} {
		t.Run(string(protocol), func(t *testing.T) {
			SetCapabilities(TerminalCapabilities{Images: protocol})
			ids := (&TUI{}).collectKittyImageIDs([]string{kittyLine, "plain"})
			if !slices.Equal(ids, []int{42}) {
				t.Fatalf("image IDs = %v, want [42]", ids)
			}
			withPrev := &TUI{prevLines: []string{"changed", "plain", kittyLine}}
			if first, last := withPrev.expandChangedRangeForKittyImages(0, 0, []string{"changed", "plain", kittyLine}); first != 0 || last != 2 {
				t.Fatalf("expanded=(%d,%d), want (0,2)", first, last)
			}
			if got := withPrev.deleteChangedKittyImages(0, 2); got != DeleteKittyImage(42) {
				t.Fatalf("delete = %q, want image 42", got)
			}
		})
	}
}
