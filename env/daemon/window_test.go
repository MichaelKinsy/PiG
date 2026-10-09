package daemon

// Ports packages/env/daemon/src/window.rs tests

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func testWindow(maxBytes, maxLines int) window {
	return window{maxBytes: maxBytes, maxLines: maxLines, minInterval: 0, bytesPerSecond: 1e12}
}

// total is the bytes and newlines delivered or skipped, and the text after the last skip.
func total(events []outputEvent) (bytes, newlines int, after string) {
	for _, event := range events {
		if event.skipped != nil {
			bytes += int(event.skipped.bytes)
			newlines += int(event.skipped.newlines)
			after = ""
		}
		bytes += len(event.text)
		newlines += newlineCount(event.text)
		after += event.text
	}
	return bytes, newlines, after
}

func TestWindowCountsEverythingAndKeepsMoreThanTheWindowAfterASkip(t *testing.T) {
	pending := newPending(testWindow(10, 2))
	var full strings.Builder
	var events []outputEvent
	for index := range 500 {
		text := fmt.Sprintf("line %d é😀\n", index)
		full.WriteString(text)
		stream := "stdout"
		if index%7 == 0 {
			stream = "stderr"
		}
		pending.push(stream, text)
		if index%50 == 49 {
			events = append(events, pending.take()...)
		}
	}
	events = append(events, pending.take()...)
	bytes, newlines, after := total(events)
	if bytes != full.Len() || newlines != newlineCount(full.String()) {
		t.Fatalf("counted %d bytes and %d newlines, want %d and %d", bytes, newlines, full.Len(), newlineCount(full.String()))
	}
	if !strings.HasSuffix(full.String(), after) {
		t.Fatal("the text after the last skip is not the end of the output")
	}
	if len(after) <= 10 && newlineCount(after) <= 2 {
		t.Fatalf("the text after the last skip (%d bytes, %d lines) is not more than the window", len(after), newlineCount(after))
	}
	skips := 0
	for _, event := range events {
		if event.skipped != nil {
			skips++
		}
	}
	if skips == 0 {
		t.Fatal("nothing was skipped")
	}
}

func TestWindowDeliversStreamsApartWithoutSkips(t *testing.T) {
	pending := newPending(testWindow(1000, 1000))
	pending.push("stdout", "a")
	pending.push("stderr", "b")
	pending.push("stdout", "c")
	var streams []string
	for _, event := range pending.take() {
		streams = append(streams, event.stream)
	}
	if strings.Join(streams, ",") != "stdout,stderr,stdout" {
		t.Fatalf("streams %v", streams)
	}
}

func TestWindowTrimsOnCharacterBoundariesAndRecordsHowTheSkipEnds(t *testing.T) {
	pending := newPending(testWindow(5, 1000))
	// A skip never splits a character: every cut text is whole characters, so the counts add up exactly.
	text := strings.Repeat("é", 100)
	pending.push("stdout", text)
	events := pending.take()
	bytes, _, after := total(events)
	if bytes != len(text) || strings.ToValidUTF8(after, "") != after || events[0].skipped == nil || events[0].skipped.endsWithNewline {
		t.Fatalf("bytes %d (want %d), after %q, skipped %+v", bytes, len(text), after, events[0].skipped)
	}
	pending.push("stdout", strings.Repeat("x\n", 100))
	events = pending.take()
	if events[0].skipped == nil || !events[0].skipped.endsWithNewline {
		t.Fatalf("a skip that ends at a newline: %+v", events[0].skipped)
	}
}

func TestWindowPacesDeliveriesLikeTheCallersCommits(t *testing.T) {
	pending := newPending(window{maxBytes: 100, maxLines: 100, minInterval: 20 * time.Millisecond, bytesPerSecond: 1000})
	pending.push("stdout", strings.Repeat("x", 50))
	before := time.Now()
	pending.take()
	// 50 bytes at 1000 bytes/s is 50 ms, more than the 20 ms minimum.
	if wait := pending.nextSend.Sub(before); wait < 45*time.Millisecond || wait > 200*time.Millisecond {
		t.Fatalf("next delivery in %s, want about 50 ms", wait)
	}
}

func TestWindowFromJSONNeedsAllFourFields(t *testing.T) {
	complete := Object{"maxBytes": float64(1000), "maxLines": float64(20), "minIntervalMs": float64(20), "bytesPerSecond": float64(1e6)}
	if got := windowFromJSON(complete); got == nil || got.maxBytes != 1000 || got.maxLines != 20 || got.minInterval != 20*time.Millisecond || got.bytesPerSecond != 1e6 {
		t.Fatalf("window %+v", got)
	}
	for key := range complete {
		partial := Object{}
		for field, value := range complete {
			if field != key {
				partial[field] = value
			}
		}
		if windowFromJSON(partial) != nil {
			t.Errorf("a window without %s was accepted", key)
		}
	}
	if windowFromJSON(nil) != nil || windowFromJSON("x") != nil {
		t.Error("a non-object window was accepted")
	}
}
