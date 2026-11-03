package tui

import (
	"bytes"
	"testing"
)

// terminal.ts parseKeyboardProtocolNegotiationSequence matches a whole reply: a reply followed by more bytes is ordinary input, so neither a Kitty flags reply nor a device attributes reply
// with trailing data is consumed, and neither changes the keyboard protocol state or settles the device attributes reply owed to the query.
func TestKeyboardProtocolNegotiationConsumesOnlyWholeReplies(t *testing.T) {
	preserveKeyboardProtocolState(t)
	var output bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &output)
	terminal.queryAndEnableKittyProtocol()
	output.Reset()
	for _, sequence := range []string{"\x1b[?7ux", "\x1b[?0ux", "\x1b[?7u\x1b[?7u", "\x1b[?62;4;52cx", "\x1b[?62;4;52c\x1b[?62;4;52c", "x\x1b[?7u", "x\x1b[?62;4;52c"} {
		if terminal.handleKeyboardProtocolNegotiationSequence(sequence) {
			t.Fatalf("%q was consumed as a negotiation reply", sequence)
		}
	}
	if IsKittyProtocolActive() || terminal.ModifyOtherKeysActive() || output.Len() != 0 {
		t.Fatalf("rejected replies changed the protocol state: kitty=%v modifyOtherKeys=%v output=%q", IsKittyProtocolActive(), terminal.ModifyOtherKeysActive(), output.String())
	}
	if owed := terminal.pendingKeyboardProtocolDeviceAttributes.Load(); owed != 1 {
		t.Fatalf("rejected replies settled the owed device attributes reply: %d owed, want 1", owed)
	}
	if !terminal.handleKeyboardProtocolNegotiationSequence("\x1b[?62;4;52c") || terminal.pendingKeyboardProtocolDeviceAttributes.Load() != 0 {
		t.Fatal("the whole device attributes reply was not consumed")
	}
}
