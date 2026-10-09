package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestExtendedKeyInitSequenceMatchesPi083(t *testing.T) {
	const want = "\x1b[?2004h\x1b[>7u\x1b[?u\x1b[c"
	if extendedKeyInit != want {
		t.Fatalf("extendedKeyInit = %q, want Pi 0.83.0 %q", extendedKeyInit, want)
	}
}

func TestKeyboardProtocolNegotiationUsesDeviceAttributesFallback(t *testing.T) {
	var output bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &output)
	// Only the DA1 reply owed to a keyboard protocol query is negotiation (terminal.ts:262,272).
	terminal.queryAndEnableKittyProtocol()
	output.Reset()
	SetKittyProtocolActive(false)
	modifyOtherKeysActive.Store(false)

	if !terminal.handleKeyboardProtocolNegotiationSequence("\x1b[?1;2c") {
		t.Fatal("device attributes response was not consumed")
	}
	if !modifyOtherKeysActive.Load() {
		t.Fatal("device attributes response did not enable modifyOtherKeys fallback")
	}
	if got := output.String(); got != "\x1b[>4;2m" {
		t.Fatalf("fallback output = %q", got)
	}
}

// terminal.ts modifyOtherKeysActive getter: false until the fallback is enabled, false again once a Kitty reply disables it.
func TestProcessTerminalModifyOtherKeysActiveFollowsNegotiation(t *testing.T) {
	preserveKeyboardProtocolState(t)
	terminal := NewProcessTerminalWithOutput(nil, nil, &bytes.Buffer{})
	if terminal.ModifyOtherKeysActive() {
		t.Fatal("modifyOtherKeys active before negotiation")
	}
	terminal.queryAndEnableKittyProtocol()
	if !terminal.handleKeyboardProtocolNegotiationSequence("\x1b[?1;2c") || !terminal.ModifyOtherKeysActive() {
		t.Fatal("device attributes reply did not enable modifyOtherKeys")
	}
	if !terminal.handleKeyboardProtocolNegotiationSequence("\x1b[?7u") {
		t.Fatal("Kitty flags reply was not consumed")
	}
	if terminal.ModifyOtherKeysActive() {
		t.Fatal("modifyOtherKeys stayed active after Kitty confirmation")
	}
}

func TestAC50KeyboardProtocolNegotiationMatchesPi083(t *testing.T) {
	var output bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &output)
	terminal.queryAndEnableKittyProtocol()
	output.Reset()
	// The negotiation marks the process-wide Kitty flag active; later tests read legacy Alt input with it off.
	t.Cleanup(func() { SetKittyProtocolActive(false); modifyOtherKeysActive.Store(false) })
	SetKittyProtocolActive(false)
	modifyOtherKeysActive.Store(true)

	if !terminal.handleKeyboardProtocolNegotiationSequence("\x1b[?7u") {
		t.Fatal("Kitty flags response was not consumed")
	}
	if !IsKittyProtocolActive() {
		t.Fatal("Kitty protocol was not marked active")
	}
	if modifyOtherKeysActive.Load() {
		t.Fatal("modifyOtherKeys remained active after Kitty confirmation")
	}
	if !strings.Contains(output.String(), "\x1b[>4;0m") {
		t.Fatalf("Kitty confirmation did not disable modifyOtherKeys: %q", output.String())
	}

	output.Reset()
	if !terminal.handleKeyboardProtocolNegotiationSequence("\x1b[?1;2c") {
		t.Fatal("device attributes response was not consumed after Kitty response")
	}
	if output.Len() != 0 {
		t.Fatalf("device attributes enabled fallback after Kitty confirmation: %q", output.String())
	}
}

func TestKeyboardProtocolNegotiationRejectsOrdinaryInput(t *testing.T) {
	terminal := NewProcessTerminalWithOutput(nil, nil, &bytes.Buffer{})
	if terminal.handleKeyboardProtocolNegotiationSequence("\x1b[A") {
		t.Fatal("ordinary arrow key was consumed as negotiation")
	}
}
