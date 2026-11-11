package runtime

import "testing"

// A worker's codemode plugin contributes its extension and sets its switch in one setup: the extensions can have composed
// before a registry's observers attach (the subscription's refresh reads them), while the switches compose after they
// attach (an observer records them in its batch). The refresh takes from its read each registry that did not compose while
// it read, so the switches composing meanwhile do not leave the extensions it read out (the first turn of a worker then
// lacked the codemode tool: "Tool codemode is not available").
func TestRefreshKeepsWhatTheReadSawOfARegistryThatDidNotComposeWhileItReadsAndWhatWasObservedOfTheOther(t *testing.T) {
	r := &durableRegistry{}
	primed, read := &KeyedTable[*P2Extension]{}, &KeyedTable[*P2Extension]{}
	r.composition.Store(&composed{table: primed})
	r.refreshFrom(func() *composed {
		// A composition of the switches, observed in its batch while the refresh reads.
		r.observed(ofSwitches, func(last *composed) *composed {
			return &composed{table: last.table, off: []string{"codemode"}, key: "codemode"}
		})
		return &composed{table: read}
	})
	got := r.composition.Load()
	if got.table != read {
		t.Error("the extensions the read saw are dropped because the switches composed while it read")
	}
	if len(got.off) != 1 || got.off[0] != "codemode" || got.key != "codemode" {
		t.Errorf("the switches observed while the read ran were replaced by its older ones: off = %q, key = %q", got.off, got.key)
	}
}

// A registry that composed while the read ran keeps its observed composition: the read's is older.
func TestRefreshKeepsWhatWasObservedWhileItReadAndTakesTheOthersFromTheRead(t *testing.T) {
	r := &durableRegistry{}
	primed, observed, read := &KeyedTable[*P2Extension]{}, &KeyedTable[*P2Extension]{}, &KeyedTable[*P2Extension]{}
	r.composition.Store(&composed{table: primed})
	r.refreshFrom(func() *composed {
		r.observed(ofExtensions, func(last *composed) *composed { return &composed{table: observed, off: last.off, key: last.key} })
		return &composed{table: read, off: []string{"agent"}, key: "agent"}
	})
	got := r.composition.Load()
	if got.table != observed {
		t.Error("the extensions observed while the read ran were replaced by its older ones")
	}
	if len(got.off) != 1 || got.off[0] != "agent" {
		t.Errorf("the switches the read saw (none composed meanwhile) are dropped: off = %q", got.off)
	}
}
