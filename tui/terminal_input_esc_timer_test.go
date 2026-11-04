package tui

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// A read that the UTF-8 decoder holds entirely decodes to "" and Node emits no data event for it, so StdinBuffer.process never runs and the ESC timer it armed keeps its original deadline. ESC at 0 ms, a lone C3 at 5 ms and A9 at 11 ms is ESC followed by é, not Alt+é, with the default 10 ms escape timeout. The test runs on synctest's virtual clock.
// Pi: packages/tui/src/terminal.ts:281-283 (the data handler calls process once per data event) and packages/tui/src/stdin-buffer.ts:321-325 (process clears the timeout), 417-418 (it re-arms it).
func TestTerminalInputHeldUTF8ReadLeavesEscTimerAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var got []string
		input := NewTerminalInput(func(sequence string) { got = append(got, sequence) })
		defer input.Close()

		input.Process([]byte{0x1b})
		time.Sleep(5 * time.Millisecond)
		input.Process([]byte{0xc3})
		time.Sleep(5 * time.Millisecond) // 10 ms: the ESC timeout expires
		input.Flush()
		time.Sleep(time.Millisecond)
		input.Process([]byte{0xa9})
		input.Flush()
		time.Sleep(time.Second)
		input.Flush()

		if want := []string{"\x1b", "é"}; !slices.Equal(got, want) {
			t.Fatalf("events %q, want %q", got, want)
		}
	})
}
