package tui

import (
	"reflect"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"
)

func TestGetTerminalColorMode(t *testing.T) {
	preserveCapabilityState(t)
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	if got := GetTerminalColorMode(); got != TerminalColorModeTrueColor {
		t.Fatalf("truecolor capabilities = %q", got)
	}
	SetCapabilities(TerminalCapabilities{})
	if got := GetTerminalColorMode(); got != TerminalColorMode256 {
		t.Fatalf("plain capabilities = %q", got)
	}
	if got := GetTerminalColorMode(TerminalCapabilities{TrueColor: true}); got != TerminalColorModeTrueColor {
		t.Fatalf("explicit capabilities = %q", got)
	}
}

// .upstream/v0.99.1/packages/tui/src/tui.ts:1122 consumeTerminalColorResponse (the 18-reply count is tui.ts:1150): a duplicate reply for a target is consumed and ignored, a reply for a palette index above 15 counts toward completion without filling the palette, and later replies are consumed until the query's DA1 reply arrives.
func TestTerminalColorQueryReplyBookkeeping(t *testing.T) {
	ui := NewWithOutput(&recordedWrites{}, 80, 24)
	query := ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1000})
	replies := []string{"\x1b]11;#ffffff\x07", "\x1b]11;#000000\x07", "\x1b]10;#808080\x07"}
	for index := range 15 {
		replies = append(replies, "\x1b]4;"+strconv.Itoa(index)+";#010203\x07")
	}
	// The 18th distinct target completes the query although palette color 15 never replied.
	replies = append(replies, "\x1b]4;99;#010203\x07")
	for _, reply := range replies {
		if !ui.ConsumeTerminalColorResponse(reply) {
			t.Fatalf("reply %q was not consumed", reply)
		}
	}
	select {
	case got := <-query:
		want := TerminalColors{Foreground: &RgbColor{R: 128, G: 128, B: 128}, Background: &whiteColor}
		if got.Err != nil || !reflect.DeepEqual(got.Colors, want) {
			t.Fatalf("result = %+v, want %+v", got, want)
		}
	default:
		t.Fatal("query did not complete after 18 distinct replies")
	}
	// Replies after completion are consumed until DA1, then release the FIFO.
	if !ui.ConsumeTerminalColorResponse("\x1b]4;15;#010203\x07") || !ui.ConsumeTerminalColorResponse(deviceAttributesReply) {
		t.Fatal("post-completion replies were not consumed")
	}
	if ui.ConsumeTerminalColorResponse(deviceAttributesReply) {
		t.Fatal("a DA1 reply with no pending query was consumed")
	}
}

func TestTerminalColorQueryWithoutLateCallbackStillConsumesUntilDA1(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui := NewWithOutput(&recordedWrites{}, 80, 24)
		query := ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1})
		time.Sleep(5 * time.Millisecond)
		if got := <-query; got.Err != nil || !reflect.DeepEqual(got.Colors, TerminalColors{}) {
			t.Fatalf("timeout result = %+v", got)
		}
		if !ui.ConsumeTerminalColorResponse("\x1b]11;#ffffff\x07") || !ui.ConsumeTerminalColorResponse(deviceAttributesReply) {
			t.Fatal("late replies were not consumed")
		}
		if ui.ConsumeTerminalColorResponse("\x1b]11;#ffffff\x07") {
			t.Fatal("reply consumed after DA1 released the query")
		}
	})
}

// .upstream/v0.99.1/packages/tui/src/terminal.ts:229 forwards the reassembled sequence of a split DA1 reply that no keyboard protocol query owns.
func TestTerminalNegotiationForwardsReassembledUnownedDeviceAttributes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTerminalNegotiationHarness(t)
		h.send("\x1b[?7u")
		h.send("\x1b[?62;4;52c")
		h.noInput(t)
		h.send("\x1b[?62;4;5")
		// The framing deadline releases the fragment to negotiation, which keeps it as a prefix.
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		h.noInput(t)
		h.send("2c")
		if got, want := h.input(), []string{"\x1b[?62;4;52c"}; !slices.Equal(got, want) {
			t.Fatalf("input=%q, want the whole reply once: %q", got, want)
		}
	})
}

func TestChooseLessDistortedCellCount(t *testing.T) {
	for _, tc := range []struct {
		upper int
		ideal float64
		want  int
	}{
		{1, 0.2, 1},
		{0, 5, 0},
		{5, 4.1, 4},
		{5, 4.9, 5},
		{5, 5.6, 5},
		{2, 1.2, 1},
	} {
		if got := chooseLessDistortedCellCount(tc.upper, tc.ideal); got != tc.want {
			t.Errorf("chooseLessDistortedCellCount(%d, %v) = %d, want %d", tc.upper, tc.ideal, got, tc.want)
		}
	}
}

// A settings list that hosts a submenu keeps keyboard focus for every mouse event the submenu handles, even one that does not request focus (settings-list.ts:180).
func TestSettingsListSubmenuMouseAlwaysFocusesTheList(t *testing.T) {
	submenu := &funcMouseComponent{lines: []string{"submenu"}, handle: func(TuiMouseEvent) *TuiMouseEventResult {
		return &TuiMouseEventResult{Handled: true}
	}}
	list := NewSettingsListWithOptions([]SettingItem{{ID: "a", Label: "A", CurrentValue: "x", Submenu: func(string, func(*string)) Component { return submenu }}}, 5, false)
	list.HandleInput("\r")
	result := DispatchMouseEvent(list, componentMouseEvent(MouseClick, 1, 0))
	if result == nil || !result.Handled || !result.Focus || result.FocusTarget != Component(list) {
		t.Fatalf("result = %+v, want a handled result focusing the list", result)
	}
	// A submenu that is not mouse-aware leaves the event unhandled.
	list = NewSettingsListWithOptions([]SettingItem{{ID: "a", Label: "A", CurrentValue: "x", Submenu: func(string, func(*string)) Component { return NewText("plain") }}}, 5, false)
	list.HandleInput("\r")
	if result := DispatchMouseEvent(list, componentMouseEvent(MouseClick, 1, 0)); result != nil {
		t.Fatalf("result = %+v, want none", result)
	}
}

// settings-list.ts:180-183 returns the submenu's own handleMouse result with focus set, so the list, not the submenu, is the dispatch target of a plain result, and even a result that reports nothing handled focuses the list.
func TestSettingsListSubmenuPlainResultTargetsTheList(t *testing.T) {
	submenu := &funcMouseComponent{lines: []string{"submenu"}, handle: func(TuiMouseEvent) *TuiMouseEventResult {
		return &TuiMouseEventResult{}
	}}
	list := NewSettingsListWithOptions([]SettingItem{{ID: "a", Label: "A", CurrentValue: "x", Submenu: func(string, func(*string)) Component { return submenu }}}, 5, false)
	list.HandleInput("\r")
	result := DispatchMouseEvent(list, componentMouseEvent(MouseClick, 1, 0))
	if result == nil || !result.Handled || !result.Focus || result.FocusTarget != Component(list) || result.Target.Component != Component(list) {
		t.Fatalf("result = %+v, want a handled result targeting and focusing the list", result)
	}
}
