package harness

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// A turn extends the kept context range by a few entries. Its cost must not carry the history's entries,
// contributions and settled messages again: only the view's one messages array, 16 bytes a message, grows with it.

func extendTurnEntries(next *durable.EntryId, turn int) []durable.EntryRecord {
	id := func() durable.EntryId { *next++; return *next }
	user := durable.EntryRecord{Id: id(), Kind: "message", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(fmt.Sprint("turn ", turn)), Timestamp: 1}}}
	call := fmt.Sprint("c", turn)
	assistant := durable.EntryRecord{Id: id(), Kind: "message", Model: []ai.Message{ai.AssistantMessage{
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}, ai.ToolCall{ID: call, Name: "lookup", Arguments: ai.JsonObject{"n": 1.0}}}, StopReason: ai.StopReasonToolUse, Timestamp: 2}}}
	result := durable.EntryRecord{Id: id(), Kind: "message", Model: []ai.Message{ai.ToolResultMessage{
		ToolCallID: call, ToolName: "lookup", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "found"}}, Timestamp: 3}}}
	return []durable.EntryRecord{user, assistant, result}
}

// grownRange is a range of turns turns, built the way reads build it: derived once, then extended turn by turn.
func grownRange(turns int, next *durable.EntryId) *contextRange {
	*next = 0
	first := []durable.EntryRecord{{Id: 1, Kind: "message", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}}}, {Id: 2, Kind: "pi.system", Model: []ai.Message{system(ai.PromptSection{Name: "s", Value: new("prompt")})}}}
	*next = 2
	rng := deriveRange(ContextBounds{Tail: 2}, first)
	for turn := range turns {
		added := extendTurnEntries(next, turn)
		rng = extendRange(rng, ContextBounds{Tail: *next}, added)
	}
	return rng
}

func extendBytesPerTurn(turns int) (bytes uint64, messages int) {
	var next durable.EntryId
	rng := grownRange(turns, &next)
	const probe = 20
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for turn := range probe {
		added := extendTurnEntries(&next, turns+turn)
		rng = extendRange(rng, ContextBounds{Tail: next}, added)
		_ = rng.shared()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / probe, len(rng.view.Messages)
}

func TestContextRangeExtensionDoesNotCopyTheHistory(t *testing.T) {
	for _, turns := range []int{500, 2000, 4000} {
		bytes, messages := extendBytesPerTurn(turns)
		t.Logf("%d turns: %d bytes per turn, %d messages", turns, bytes, messages)
		// One messages array (16 bytes a message) plus a constant for the turn's own entries.
		if limit := uint64(20*messages + 16384); bytes > limit {
			t.Errorf("%d turns: %d bytes per extension, want at most %d: the extension copies more than the messages", turns, bytes, limit)
		}
	}
}

func TestContextRangeExtensionsFromOneRangeNeverWriteEachOther(t *testing.T) {
	var next durable.EntryId
	base := grownRange(300, &next)
	baseSnapshot := base.shared()
	baseEntries := append([]durable.EntryRecord(nil), baseSnapshot.Entries...)
	baseContributions := append([][]ai.Message(nil), baseSnapshot.Contributions...)
	baseMessages := append([]ai.Message(nil), baseSnapshot.Messages...)
	const forks = 16
	type fork struct {
		rng     *contextRange
		view    durable.ContextView
		entries []durable.EntryRecord
	}
	results := make([]fork, forks)
	var group sync.WaitGroup
	for index := range forks {
		group.Go(func() {
			// Every fork numbers its entries from the same tail, as concurrent reads of the same conversation do.
			local := next
			rng := base
			var all []durable.EntryRecord
			for turn := range 40 {
				added := extendTurnEntries(&local, index*1000+turn)
				all = append(all, added...)
				rng = extendRange(rng, ContextBounds{Tail: local}, added)
				view := rng.shared()
				if len(view.Entries) != len(baseEntries)+len(all) {
					t.Errorf("fork %d: %d entries", index, len(view.Entries))
					return
				}
			}
			results[index] = fork{rng: rng, view: rng.shared(), entries: all}
		})
	}
	group.Wait()
	for index, got := range results {
		whole := append(append([]durable.EntryRecord(nil), base.entries...), got.entries...)
		want := deriveRange(ContextBounds{Tail: got.rng.bounds.Tail}, whole)
		if !reflect.DeepEqual(got.view, want.shared()) {
			t.Fatalf("fork %d: the extended view differs from the whole derivation", index)
		}
	}
	if !reflect.DeepEqual(base.shared().Entries, baseEntries) || !reflect.DeepEqual(base.shared().Contributions, baseContributions) || !reflect.DeepEqual(base.shared().Messages, baseMessages) {
		t.Fatal("extensions changed the range they extended")
	}
}

// Readers of an earlier view run while later reads extend the range in place; run with -race.
func TestContextRangeViewsAreReadableWhileTheRangeExtends(t *testing.T) {
	var next durable.EntryId
	rng := grownRange(50, &next)
	var group sync.WaitGroup
	for range 8 {
		view := rng.shared()
		group.Go(func() {
			count := 0
			for range 200 {
				for index := range view.Entries {
					count += int(view.Entries[index].Id)
				}
				for _, contribution := range view.Contributions {
					count += len(contribution)
				}
				count += len(view.Messages)
			}
			_ = count
		})
	}
	for turn := range 400 {
		added := extendTurnEntries(&next, turn)
		rng = extendRange(rng, ContextBounds{Tail: next}, added)
	}
	group.Wait()
}

func BenchmarkContextRangeExtend(b *testing.B) {
	for _, turns := range []int{500, 2000, 4000} {
		b.Run(fmt.Sprint(turns, "turns"), func(b *testing.B) {
			var next durable.EntryId
			rng := grownRange(turns, &next)
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				added := extendTurnEntries(&next, turns+i)
				rng = extendRange(rng, ContextBounds{Tail: next}, added)
				_ = rng.shared()
			}
		})
	}
}
