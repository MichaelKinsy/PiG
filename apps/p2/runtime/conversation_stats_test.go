package runtime

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/test/p2/conversation-stats.test.ts: the active context's numbers kept from appended entries
// (`ContextTally`) equal pi-ai's estimate over the whole context, and ask for a whole context when the range changes
// otherwise.

func statsUsage(total int) ai.Usage { return ai.Usage{Input: total, TotalTokens: total} }

func statsUser(text string) ai.Message {
	return ai.UserMessage{Content: ai.UserText(text), Timestamp: 1}
}

func statsAnswer(text string, total int, stop ai.StopReason) ai.Message {
	return ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: stop, Usage: statsUsage(total), Timestamp: 2}
}

func statsEntry(id int, message ai.Message) durable.EntryRecord {
	entry := durable.EntryRecord{Id: durable.EntryId(id), ConversationId: 1, Kind: "x"}
	if message != nil {
		entry.Model = []ai.Message{message}
	}
	return entry
}

func statsWindow(entries []durable.EntryRecord, before *int) harness.ConversationView {
	view := harness.ConversationView{Entries: entries}
	if before != nil {
		id := durable.EntryId(*before)
		view.Before = &id
	}
	return view
}

// statsContext is a context of entries as Durable builds it: their messages, without aborted, failed and deferred
// responses.
func statsContext(entries []durable.EntryRecord) durable.ContextView {
	view := durable.ContextView{Entries: entries}
	for _, entry := range entries {
		for _, message := range entry.Model {
			if response, ok := message.(ai.AssistantMessage); ok {
				switch response.StopReason {
				case ai.StopReasonAborted, ai.StopReasonError, ai.StopReasonDeferred:
					continue
				}
			}
			view.Messages = append(view.Messages, message)
		}
	}
	return view
}

func TestAppendedEntriesGiveWhatPiAIsEstimateGivesOverTheWholeContext(t *testing.T) {
	all := []durable.EntryRecord{
		statsEntry(1, statsUser("first question")),
		statsEntry(2, statsAnswer("first answer", 1000, ai.StopReasonStop)),
		statsEntry(3, statsUser(strings.Repeat("x", 350))),
		statsEntry(5, statsAnswer("failed", 5000, ai.StopReasonError)),
		statsEntry(6, nil),
		statsEntry(7, statsAnswer("second answer", 2000, ai.StopReasonStop)),
		statsEntry(8, statsUser(strings.Repeat("y", 700))),
		statsEntry(9, statsAnswer("no usage", 0, ai.StopReasonStop)),
	}
	tally := NewContextTally()
	tally.Reset(statsContext(all[:2]))
	for end := 3; end <= len(all); end++ {
		var before *int
		if end-4 >= 0 {
			id := int(all[end-4].Id)
			before = &id
		}
		if !tally.Advance(statsWindow(all[max(0, end-3):end], before)) {
			t.Fatalf("end %d: advance asked for a whole context", end)
		}
		want := ai.EstimateContextTokens(statsContext(all[:end]).Messages).Tokens
		if got := tally.Stats().ContextTokens; got != want {
			t.Fatalf("end %d: contextTokens = %d, want %d", end, got, want)
		}
	}
	// The latest response's usage, also a failed one, as the cost line shows it.
	if latest := tally.Stats().LatestAssistantUsage; latest == nil || !reflect.DeepEqual(*latest, statsUsage(0)) {
		t.Fatalf("latestAssistantUsage = %v, want %v", latest, statsUsage(0))
	}
	// A view without new entries changes nothing.
	before := tally.Stats()
	seven := 7
	if !tally.Advance(statsWindow(all[len(all)-2:], &seven)) {
		t.Fatal("a view without new entries asked for a whole context")
	}
	if got := tally.Stats(); !reflect.DeepEqual(got, before) {
		t.Fatalf("stats changed: %v, was %v", got, before)
	}
}

func TestACompactionAResetAnEditOrSkippedEntriesNeedAWholeContext(t *testing.T) {
	base := []durable.EntryRecord{statsEntry(1, statsUser("q")), statsEntry(2, statsAnswer("a", 100, ai.StopReasonStop))}
	fresh := func() *ContextTally {
		tally := NewContextTally()
		tally.Reset(statsContext(base))
		return tally
	}
	with := func(entry durable.EntryRecord) []durable.EntryRecord {
		return append(append([]durable.EntryRecord{}, base...), entry)
	}
	head := durable.EntryId(1)
	compaction := statsEntry(3, statsUser("s"))
	compaction.Head = &head
	if fresh().Advance(statsWindow(with(compaction), nil)) {
		t.Error("a new head must need a whole context")
	}
	edit := statsEntry(3, nil)
	edit.Edits = []durable.ContextEdit{{}}
	if fresh().Advance(statsWindow(with(edit), nil)) {
		t.Error("an edit must need a whole context")
	}
	// Entries between the newest taken and the window's start were never seen.
	eight, two := 8, 2
	if fresh().Advance(statsWindow([]durable.EntryRecord{statsEntry(9, statsUser("later"))}, &eight)) {
		t.Error("skipped entries must need a whole context")
	}
	if !fresh().Advance(statsWindow([]durable.EntryRecord{statsEntry(3, statsUser("next"))}, &two)) {
		t.Error("the next entry must simply append")
	}
}

// ResponseCosts is each assistant response's cost, with the cache hit rate when the response reported prompt tokens.
func TestResponseCostsListsResponsesWithTheirCacheHit(t *testing.T) {
	cached := statsAnswer("a", 100, ai.StopReasonStop).(ai.AssistantMessage)
	cached.Usage = ai.Usage{Input: 50, CacheRead: 50, TotalTokens: 100, Cost: ai.UsageCost{Total: 0.25}}
	cached.Provider, cached.Model = "faux", "m"
	silent := statsAnswer("b", 0, ai.StopReasonError).(ai.AssistantMessage)
	silent.Usage = ai.Usage{}
	costs := ResponseCosts([]durable.EntryRecord{statsEntry(1, statsUser("q")), statsEntry(4, cached), statsEntry(7, silent)})
	if len(costs) != 2 {
		t.Fatalf("costs = %v", costs)
	}
	if costs[0].EntryID != "4" || costs[0].Cost != 0.25 || costs[0].CacheHit == nil || *costs[0].CacheHit != 0.5 || costs[0].Provider != "faux" || costs[0].StopReason != "stop" {
		t.Errorf("first = %+v", costs[0])
	}
	if costs[1].EntryID != "7" || costs[1].CacheHit != nil || costs[1].StopReason != "error" {
		t.Errorf("second = %+v", costs[1])
	}
}

// FuzzContextTallyAdvance: any window over any entries never panics, and a tally that accepted a window reports the
// estimate of the messages it took (never negative).
func FuzzContextTallyAdvance(f *testing.F) {
	f.Add(uint8(3), uint8(1), uint8(0), 100)
	f.Add(uint8(9), uint8(5), uint8(2), 0)
	f.Fuzz(func(t *testing.T, count, before, shape uint8, total int) {
		var entries []durable.EntryRecord
		for i := 1; i <= int(count%16); i++ {
			var message ai.Message = statsUser("x")
			if (int(shape)+i)%3 == 0 {
				message = statsAnswer("a", total%10000, ai.StopReasonStop)
			}
			entries = append(entries, statsEntry(i, message))
		}
		tally := NewContextTally()
		tally.Reset(statsContext(nil))
		id := int(before)
		tally.Advance(statsWindow(entries, &id))
		if tally.Stats().ContextTokens < 0 && total >= 0 {
			t.Fatalf("negative context tokens %d", tally.Stats().ContextTokens)
		}
	})
}
