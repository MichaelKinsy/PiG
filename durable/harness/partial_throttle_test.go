package harness

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// A partial commit appends the text the response's events added to the committed string when the lengths agree, so the text it appends must be
// the text of the partial it commits: generation.ts copies the provider's partial and takes the changes in one tick, while here the events
// arrive on another goroutine than the flush. These tests pin the pairing of the partial and the changes.

// testMessage is a partial message holding text as its one block.
func testMessage(text string) *ai.AssistantMessage {
	return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: "test", Provider: "test", Model: "test", StopReason: ai.StopReasonPending}
}

func TestPartialChangesACutLeavesTheChangesOfLaterEventsToTheNextCut(t *testing.T) {
	stream := newPartialStream(t)
	stream.text("abcd")
	cut := stream.changes.Cut()
	// Observed after the cut, so the partial the cut is bound to does not hold it.
	stream.text("efgh")
	stream.commitCut(cut, testMessage("abcd"), false)
	if text, _ := stream.committed(); text != "abcd" {
		t.Fatalf("committed %q, want abcd", text)
	}
	stream.commit(false)
	text, _ := stream.committed()
	want := []delta.Op{{"a", []any{"generation", "message", "content", 0, "text"}, "efgh"}}
	if ops := stream.ops[len(stream.ops)-1]; text != "abcdefgh" || !reflect.DeepEqual(ops, want) {
		t.Fatalf("committed %q by %v, want abcdefgh by %v", text, ops, want)
	}
}

func TestPartialChangesABlockTheBoundPartialLacksKeepsItsChangesForTheNextCut(t *testing.T) {
	stream := newPartialStream(t)
	stream.argument(`{"a":"x`)
	stream.commitCut(stream.changes.Cut(), testMessage(""), false)
	stream.argument(`y"}`)
	stream.commit(false)
	if _, got := stream.committed(); partialJSONOf(t, got) != `{"a":"xy"}` {
		t.Fatalf("committed %s", partialJSONOf(t, got))
	}
}

func TestPartialChangesUncutPutsBackTheArgumentChangesOfACutThatNoCommitUsed(t *testing.T) {
	stream := newPartialStream(t)
	stream.argument(`{"a":"x`)
	stream.changes.Uncut(stream.changes.Cut())
	stream.argument(`y"}`)
	stream.commit(false)
	if _, got := stream.committed(); partialJSONOf(t, got) != `{"a":"xy"}` {
		t.Fatalf("committed %s", partialJSONOf(t, got))
	}
}

// timerlessRuntime is what a throttle's observe uses of a generation's runtime: it arms a timer, which nothing fires.
type timerlessRuntime struct{ generationRuntime }

func (timerlessRuntime) SetTimer(func(), float64) func() { return func() {} }

// Events are observed while partials are taken, as the response's events and the flush do: whatever is taken, the partial holds exactly the
// text of the events taken up to it. A partial one event behind or ahead of the changes commits characters no stream produced, when the
// string grew by as much as the changes did.
func TestPartialThrottleTakesAPartialWithExactlyTheChangesOfTheEventsUpToIt(t *testing.T) {
	const events = 4000
	throttle := &partialThrottle{runtime: timerlessRuntime{}, interval: 100, changes: NewPartialChanges()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		text := ""
		for index := range events {
			piece := fmt.Sprintf("%04d", index)
			text += piece
			partial := testMessage(text)
			throttle.observe(ai.TextDeltaEvent{ContentIndex: 0, Delta: piece, Partial: partial}, partial)
			runtime.Gosched()
		}
	}()
	var seen strings.Builder
	takes := 0
	take := func() {
		throttle.mu.Lock()
		partial, cut, ok := throttle.takeLocked()
		throttle.mu.Unlock()
		if !ok {
			return
		}
		takes++
		if pending := cut.text[0]; pending != nil {
			seen.Write(pending.text)
		}
		if got := pinned(&partial).Content[0].(ai.TextContent).Text; got != seen.String() {
			t.Fatalf("take %d: the partial holds %d bytes of text, and the events taken up to it %d bytes", takes, len(got), seen.Len())
		}
	}
	for finished := false; !finished; {
		select {
		case <-done:
			finished = true
		default:
		}
		take()
		runtime.Gosched()
	}
	take()
	if takes < 2 || seen.Len() != 4*events {
		t.Fatalf("%d takes covered %d bytes of text, want more than one covering %d", takes, seen.Len(), 4*events)
	}
}
