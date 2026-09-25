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

func TestAC50KeyboardProtocolNegotiationMatchesPi083(t *testing.T) {
	var output bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &output)
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
