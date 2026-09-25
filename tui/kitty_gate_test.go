package tui

import "testing"

// A rendered Kitty image line carries the \x1b_G graphics APC with an i=<id>
// parameter. On a Kitty terminal the per-frame scans must find it; on any other
// terminal that sequence can never be emitted, so the scans must short-circuit
// without walking the buffer (the O(total lines) cost that dominated
// long-session frames).
func TestKittyImageScansGatedOnCapability(t *testing.T) {
	prev := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(prev) })

	kittyLine := "\x1b_Gi=42,a=T;payload\x1b\\"

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty})
	ids := (&TUI{}).collectKittyImageIDs([]string{kittyLine, "plain"})
	if _, ok := ids[42]; !ok || len(ids) != 1 {
		t.Fatalf("kitty terminal must detect image id 42, got %v", ids)
	}

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2})
	if ids := (&TUI{}).collectKittyImageIDs([]string{kittyLine, "plain"}); len(ids) != 0 {
		t.Fatalf("non-kitty terminal must skip kitty scan, got %v", ids)
	}

	// expandChangedRangeForKittyImages / deleteChangedKittyImages read prevLines.
	// With a kitty image at row 2 and a change at row 0, the kitty path must
	// expand lastChanged to include the image row and emit a delete sequence;
	// the non-kitty path must do neither.
	withPrev := &TUI{prevLines: []string{"changed", "plain", kittyLine}}

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty})
	if first, last := withPrev.expandChangedRangeForKittyImages(0, 0, []string{"changed", "plain", kittyLine}); first != 0 || last != 2 {
		t.Fatalf("kitty expand = (%d,%d), want (0,2)", first, last)
	}
	if got := withPrev.deleteChangedKittyImages(0, 2); got == "" {
		t.Fatalf("kitty delete must emit a delete sequence, got empty")
	}

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2})
	if first, last := withPrev.expandChangedRangeForKittyImages(0, 0, []string{"changed", "plain", kittyLine}); first != 0 || last != 0 {
		t.Fatalf("non-kitty expand = (%d,%d), want (0,0)", first, last)
	}
	if got := withPrev.deleteChangedKittyImages(0, 2); got != "" {
		t.Fatalf("non-kitty delete must be empty; got %q", got)
	}
}
