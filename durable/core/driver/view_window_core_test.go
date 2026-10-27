// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The conversation view's window (spec §9.3) over the core: harness-view.test.ts "window" group, whose first value, frames and
// ops the core computes (view.go). The core delivers frames on its own goroutine, so a test waits until the newest one arrived.

// windowNotes appends count entries of kind note, one per commit, and returns them.
func windowNotes(t *testing.T, conversation harness.Conversation, count int) []durable.EntryRecord {
	t.Helper()
	entries := make([]durable.EntryRecord, 0, count)
	for index := range count {
		result, err := conversation.Commit(t.Context(), func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: "note", Data: index})
		})
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, result.(durable.EntryRecord))
	}
	return entries
}

func windowIds(entries []durable.EntryRecord) []durable.EntryId {
	ids := make([]durable.EntryId, len(entries))
	for index, entry := range entries {
		ids[index] = entry.Id
	}
	return ids
}

func expectWindow(t *testing.T, view harness.ConversationView, want []durable.EntryRecord, before *durable.EntryRecord) {
	t.Helper()
	if got := windowIds(view.Entries); !slices.Equal(got, windowIds(want)) {
		t.Fatalf("window entries %v, want %v", got, windowIds(want))
	}
	switch {
	case before == nil && view.Before != nil:
		t.Fatalf("before = %d, want none", *view.Before)
	case before != nil && (view.Before == nil || *view.Before != before.Id):
		t.Fatalf("before = %v, want %d", view.Before, before.Id)
	}
}

// windowOf is the first value of a watch of root with window, which is then stopped.
func windowOf(t *testing.T, root harness.Conversation, window *harness.ConversationWindow) harness.ConversationView {
	t.Helper()
	watch, err := root.Watch(t.Context(), window)
	if err != nil {
		t.Fatal(err)
	}
	view := watch.Value()
	if _, err := watch.Stop(); err != nil {
		t.Fatal(err)
	}
	return view
}

// harness-view.test.ts:376: a window starts with the newest `min` entries and the entry before them, or the whole short transcript.
func TestCoreViewWindowStartsWithTheNewestMinEntriesAndTheEntryBeforeThem(t *testing.T) {
	s := openCore(t)
	root, err := s.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	written := windowNotes(t, root, 10)
	expectWindow(t, windowOf(t, root, &harness.ConversationWindow{Min: new(4)}), written[6:], &written[5])
	// The default window: the newest 50, here all ten, starting the transcript.
	expectWindow(t, windowOf(t, root, nil), written, nil)
}

// harness-view.test.ts:390: a window takes appends up to `max`, then drops its oldest in one splice down to `min`, and a replay of the
// ops of every frame gives the value of a fresh watch.
func TestCoreViewWindowDropsItsOldestPastMaxInOneSplice(t *testing.T) {
	s := openCore(t)
	root, err := s.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	written := windowNotes(t, root, 4)
	watch, err := root.Watch(t.Context(), &harness.ConversationWindow{Min: new(3), Max: new(5)})
	if err != nil {
		t.Fatal(err)
	}
	initial := watch.Value()
	expectWindow(t, initial, written[1:], &written[0])
	var mu sync.Mutex
	var frames []chord.WatchChange[harness.ConversationView]
	watch.Start(func(_ context.Context, change chord.WatchChange[harness.ConversationView]) error {
		mu.Lock()
		defer mu.Unlock()
		frames = append(frames, change)
		return nil
	})
	appended := windowNotes(t, root, 3)
	pollUntil(t, "the frames of the three commits", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(frames) > 0 && slices.Equal(windowIds(frames[len(frames)-1].Value.Entries), windowIds(appended))
	})
	mu.Lock()
	got := slices.Clone(frames)
	mu.Unlock()
	if _, err := watch.Stop(); err != nil {
		t.Fatal(err)
	}
	// The listener may be called with several commits folded; the last value is the window after all three.
	last := got[len(got)-1]
	expectWindow(t, last.Value, appended, &written[3])
	// The window grew to its max of 5 and the third append dropped down to 3: the last frame ends with the splice and `before`.
	ops := last.Ops
	if len(ops) < 2 || delta.Verb(ops[len(ops)-1]) != "s" || delta.PathOf(ops[len(ops)-1])[0] != "before" {
		t.Fatalf("ops of the last frame %v, want them to end with a set of before", ops)
	}
	// A fresh watch holds the same window.
	expectWindow(t, windowOf(t, root, &harness.ConversationWindow{Min: new(3), Max: new(5)}), appended, &written[3])
}

// harness-view.test.ts:418: a commit that appends more than `max` at once leaves the newest `min`.
func TestCoreViewWindowKeepsTheNewestMinOfACommitThatAppendsMoreThanMax(t *testing.T) {
	s := openCore(t)
	root, err := s.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	windowNotes(t, root, 2)
	watch, err := root.Watch(t.Context(), &harness.ConversationWindow{Min: new(2), Max: new(3)})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var latest harness.ConversationView
	watch.Start(func(_ context.Context, change chord.WatchChange[harness.ConversationView]) error {
		mu.Lock()
		defer mu.Unlock()
		latest = change.Value
		return nil
	})
	result, err := root.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		var entries []durable.EntryRecord
		for range 5 {
			entry, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "burst"})
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		}
		return entries, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	burst := result.([]durable.EntryRecord)
	pollUntil(t, "the frame of the burst", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(latest.Entries) > 0
	})
	mu.Lock()
	view := latest
	mu.Unlock()
	if _, err := watch.Stop(); err != nil {
		t.Fatal(err)
	}
	expectWindow(t, view, burst[3:], &burst[2])
}

// harness-view.test.ts:436: a window starts at `from` when the entries since fit in `max`, otherwise at the newest `min`; a `from`
// past the newest entry starts it empty, right after it.
func TestCoreViewWindowStartsAtFromWhenTheEntriesSinceFitInMax(t *testing.T) {
	s := openCore(t)
	root, err := s.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	written := windowNotes(t, root, 30)
	window := func(from durable.EntryId) *harness.ConversationWindow {
		return &harness.ConversationWindow{Min: new(5), Max: new(10), From: &from}
	}
	// Ten entries since `from`: all of them, with the entry before `from`.
	expectWindow(t, windowOf(t, root, window(written[20].Id)), written[20:], &written[19])
	// Fewer than `min` since `from`: only those.
	expectWindow(t, windowOf(t, root, window(written[28].Id)), written[28:], &written[27])
	// Resuming after the newest entry: an empty window right after it.
	expectWindow(t, windowOf(t, root, window(written[29].Id+1)), nil, &written[29])
	// Eleven since `from`: more than `max`, so the newest `min`; `before` shows the client a gap.
	expectWindow(t, windowOf(t, root, window(written[19].Id)), written[25:], &written[24])
	// From the transcript's start.
	expectWindow(t, windowOf(t, root, &harness.ConversationWindow{Min: new(40), From: &written[0].Id}), written, nil)
}

// harness-view.test.ts:499: window bounds that are not integers or put `max` below `min` are rejected, on both attachments.
func TestCoreViewWindowRejectsBoundsOutOfRange(t *testing.T) {
	s := openCore(t)
	root, err := s.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, window := range map[string]*harness.ConversationWindow{
		"min 0":         {Min: new(0)},
		"max below min": {Min: new(10), Max: new(9)},
	} {
		if _, err := root.Watch(t.Context(), window); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if _, err := s.ObserveConversation(t.Context(), root.Id(), &viewRecording{t: t}, &harness.ConversationWindow{Max: new(0)}); err == nil {
		t.Fatal("observeConversation accepted a max of 0")
	}
	// `max` may equal `min`.
	watch, err := root.Watch(t.Context(), &harness.ConversationWindow{Min: new(3), Max: new(3)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Stop(); err != nil {
		t.Fatal(err)
	}
}

// harness-view.test.ts:511: each observer has its own window.
func TestCoreViewWindowGivesEachObserverItsOwnWindow(t *testing.T) {
	s := openCore(t)
	root, err := s.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	written := windowNotes(t, root, 6)
	observed := &viewRecording{t: t}
	observation, err := s.ObserveConversation(t.Context(), root.Id(), observed, &harness.ConversationWindow{Min: new(2), Max: new(3)})
	if err != nil {
		t.Fatal(err)
	}
	wide := windowOf(t, root, &harness.ConversationWindow{Min: new(5)})
	expectWindow(t, observation.Value(), written[4:], &written[3])
	expectWindow(t, wide, written[1:], &written[0])
	next := windowNotes(t, root, 1)[0]
	pollUntil(t, "the narrow observer's frame", func() bool { return len(observed.All()) > 0 })
	observation.Detach()
	expectWindow(t, observed.All()[0], append(slices.Clone(written[4:]), next), &written[3])
}
