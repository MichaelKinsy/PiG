// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package tui

import (
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// terminal-colors.test.ts (Pi 1.2.0) "consumes delayed Ghostty color fragments without typing their tail or opening the editor":
// the production TerminalInput (StdinBuffer and its timer) feeds the TUI's terminal input dispatcher.
func TestUpstreamQueryTerminalColorsConsumesDelayedGhosttyColorFragments(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui := NewWithOutput(&recordedWrites{}, 80, 24)
		component := &inputRecorder{}
		ui.Add(component)
		ui.SetFocus(component)
		h := newStdinSecondHalfOwner(t, StdinBufferOptions{})
		delivered := 0
		send := func(data string) {
			h.process(data)
			h.mu.Lock()
			pending := slices.Clone(h.data[delivered:])
			delivered = len(h.data)
			h.mu.Unlock()
			for _, sequence := range pending {
				ui.HandleTerminalInput(sequence)
			}
		}
		query := ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1000})
		// Captured from Ghostty 1.3.1 with Ayu Light, split at the reported tail.
		send("\x1b]4;1;rgb:eaea/6c")
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		send("6c/6d6d\x07" + deviceAttributesReply)
		select {
		case got := <-query:
			if got.Err != nil || !reflect.DeepEqual(got.Colors, TerminalColors{}) {
				t.Fatalf("result = %+v, want no colors", got)
			}
		default:
			t.Fatal("query did not resolve on DA1")
		}
		if len(component.inputs) != 0 {
			t.Fatalf("component inputs = %q, want none", component.inputs)
		}
		// A real Ctrl+G must still reach the editor after the reply.
		send("\x07")
		if !slices.Equal(component.inputs, []string{"\x07"}) {
			t.Fatalf("component inputs = %q, want Ctrl+G", component.inputs)
		}
	})
}
