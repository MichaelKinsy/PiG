// Ports packages/durable/test/examples/31-transcript.ts: a client's transcript over the windowed conversation view.
// `ConversationTranscript` merges every value of a watch into the client's whole transcript, marks entries a stall skipped as a
// gap, and loads them, and earlier pages, on demand; a client that drops resumes after the newest entry it has. The script prints one
// line per step; this test asserts those lines. Upstream's `settle()` (a macrotask) becomes waiting for the watch to deliver the
// newest entry, because Go delivers a watch's values on a goroutine of its own.

package examples_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// renderTranscript is the script's render: one line per item range: ranges of entries, gaps, and the "load earlier" marker.
func renderTranscript(label string, view *harness.TranscriptView) string {
	var rows []string
	first, last := 0, 0
	inRun := false
	flush := func() {
		if !inRun {
			return
		}
		if first == last {
			rows = append(rows, strconv.Itoa(first))
		} else {
			rows = append(rows, fmt.Sprintf("%d..%d", first, last))
		}
		inRun = false
	}
	for _, item := range view.Items {
		if entry, ok := item.(harness.TranscriptEntry); ok {
			line, _ := strconv.Atoi(strings.TrimPrefix(entry.Entry.Data.(string), "line "))
			if inRun {
				last = line
			} else {
				first, last, inRun = line, line, true
			}
			continue
		}
		flush()
		if _, isGap := item.(harness.TranscriptGap); isGap {
			rows = append(rows, "[load more]")
		} else {
			rows = append(rows, "[load earlier]")
		}
	}
	flush()
	return fmt.Sprintf("%-28s %s", label, strings.Join(rows, " "))
}

func firstTranscriptGap(view *harness.TranscriptView) (harness.TranscriptGap, bool) {
	for _, item := range view.Items {
		if gap, ok := item.(harness.TranscriptGap); ok {
			return gap, true
		}
	}
	return harness.TranscriptGap{}, false
}

// 31-transcript.ts
// Pi source: packages/durable/src/harness/transcript.ts, packages/durable/src/harness/view.ts
func TestExample31Transcript(t *testing.T) {
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: ai.CreateModels(), Registry: harness.CreateRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, nil))
	count := 0
	// chat commits n chat lines, one entry each.
	chat := func(n int) {
		for range n {
			count++
			text := fmt.Sprintf("line %d", count)
			commit(t, root, func(tx durable.Tx) (durable.EntryRecord, error) {
				return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "chat", Data: text})
			})
		}
	}
	var printed []string
	say := func(label string, view *harness.TranscriptView) {
		printed = append(printed, renderTranscript(label, view))
	}

	chat(200)

	// Attach: the window holds the newest `min` entries; the transcript shows "load earlier" above them.
	transcript, err := harness.NewConversationTranscript(func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		return root.Entries(ctx, query, limit, nil)
	}, &harness.ConversationTranscriptOptions{PageSize: new(20)})
	if err != nil {
		t.Fatal(err)
	}
	window := harness.ConversationWindow{Min: new(10), Max: new(20)}
	var mu sync.Mutex
	var view *harness.TranscriptView
	current := func() *harness.TranscriptView {
		mu.Lock()
		defer mu.Unlock()
		return view
	}
	set := func(next *harness.TranscriptView) {
		mu.Lock()
		defer mu.Unlock()
		view = next
	}
	// follow starts the watch's merging.
	follow := func(watch harness.ConversationWatch) {
		watch.Start(func(_ context.Context, change chord.WatchChange[harness.ConversationView]) error {
			merged := transcript.Merge(change.Value)
			mu.Lock()
			defer mu.Unlock()
			view = merged
			return nil
		})
	}
	// settle waits until the watch delivered the commit of line n.
	settle := func(n int) {
		t.Helper()
		eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			if view == nil {
				return false
			}
			entries := view.Items
			if len(entries) == 0 {
				return false
			}
			last, ok := entries[len(entries)-1].(harness.TranscriptEntry)
			return ok && last.Entry.Data == fmt.Sprintf("line %d", n)
		})
	}
	watch := must(root.Watch(background, &window))
	set(transcript.Merge(watch.Value()))
	say("attach", current())
	follow(watch)

	// Live: appends. The window drops its oldest past `max`; the transcript keeps them.
	chat(15)
	settle(215)
	say("15 more, live", current())

	// The user scrolls up: one page.
	earlier := must(transcript.LoadEarlier(background))
	set(earlier)
	say("load earlier", earlier)

	// A drop: the client misses 40 entries, more than `max`, and resumes after the newest it has. The window starts at the
	// newest `min` again, and `before` shows the transcript a gap.
	must(watch.Stop())
	chat(40)
	resumed := window
	resumed.From = transcript.Resume().From
	watch = must(root.Watch(background, &resumed))
	set(transcript.Merge(watch.Value()))
	say("resume after 40 missed", current())
	follow(watch)

	// "load more" fills the gap a page at a time.
	for gap, found := firstTranscriptGap(current()); found; gap, found = firstTranscriptGap(current()) {
		filled := must(transcript.FillGap(background, gap))
		set(filled)
		say("fill gap", filled)
	}

	// A short drop resumes without a gap: the window starts right after the newest entry.
	must(watch.Stop())
	chat(3)
	resumed.From = transcript.Resume().From
	watch = must(root.Watch(background, &resumed))
	printed = append(printed, fmt.Sprintf("%-28s window: %d entries", "resume after 3 missed", len(watch.Value().Entries)))
	set(transcript.Merge(watch.Value()))
	say("", current())

	must(watch.Stop())
	closeSession(t, opened)

	expectEqual(t, "printed lines", printed, []string{
		"attach                       [load earlier] 191..200",
		"15 more, live                [load earlier] 191..215",
		"load earlier                 [load earlier] 171..215",
		"resume after 40 missed       [load earlier] 171..215 [load more] 246..255",
		"fill gap                     [load earlier] 171..235 [load more] 246..255",
		"fill gap                     [load earlier] 171..255",
		"resume after 3 missed        window: 3 entries",
		"                             [load earlier] 171..258",
	})
}
