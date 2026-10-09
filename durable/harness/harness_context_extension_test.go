// Ports the case "derives the same view from extended reads as from whole reads" of the Pi 1.1.0
// packages/durable/test/harness-context.test.ts (src/harness/context.ts readContextFrom, extendRange, settle).
//
// Pi's reads reuse a kept range inside a task runtime; PiG's derived-context cache (context_cache.go) does the same for
// stores that retain decoded entries, so the case runs over SQLite, whose reads the cache serves, and over MemoryStorage,
// whose reads derive from scratch. Each read through a task runtime must equal the whole read of the same conversation.

package harness

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func TestContextExtendedReadsMatchWholeReads(t *testing.T) {
	stores := map[string]func(*testing.T) durable.Storage{
		"SqliteStorage": func(t *testing.T) durable.Storage { return tkOpenSqlite(t, sqlitePath(t)) },
		"MemoryStorage": func(*testing.T) durable.Storage { return storage.NewMemoryStorage() },
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) { runExtendedReads(t, open(t)) })
	}
}

func runExtendedReads(t *testing.T, store durable.Storage) {
	// Deterministic pseudo-random transcript (32-bit LCG, high bits): calls and results in any order, missing and stray
	// results, excluded stop reasons, system entries, notes, edits of earlier entries, and head markers.
	seed := uint32(7)
	random := func(n int) int {
		seed = seed*1_664_525 + 1_013_904_223
		return int(float64(seed) / (1 << 32) * float64(n))
	}
	seen := map[string]int{}
	var failure error
	var rootConversation Conversation
	steps := tkOneStep("test.context-steps", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		var written []durable.EntryRecord
		var calls []string
		next := 0
		draft := func() durable.EntryDraft {
			pick := random(14)
			switch {
			case pick < 3:
				seen["user"]++
				next++
				return durable.EntryDraft{Kind: "message", Model: []ai.Message{user(fmt.Sprintf("u%d", next-1))}}
			case pick < 5:
				ids := make([]string, random(3))
				for index := range ids {
					ids[index] = fmt.Sprintf("c%d", next)
					next++
				}
				calls = append(calls, ids...)
				stop := []ai.StopReason{"aborted", "error", "deferred", "", "", "", "", "", ""}[random(9)]
				category := string(stop)
				if stop == "" {
					category = "assistant"
				}
				seen[category]++
				text := fmt.Sprintf("a%d", next)
				next++
				return durable.EntryDraft{Kind: "message", Model: []ai.Message{assistant(text, assistantOptions{calls: ids, stopReason: stop})}}
			case pick < 8:
				matched := len(calls) > 0 && random(5) > 0
				var id string
				if matched {
					seen["result"]++
					index := random(len(calls))
					id = calls[index]
					calls = append(calls[:index], calls[index+1:]...)
				} else {
					seen["stray result"]++
					id = fmt.Sprintf("stray%d", next)
					next++
				}
				return durable.EntryDraft{Kind: "message", Model: []ai.Message{toolResult(id)}}
			case pick < 9:
				seen["system"]++
				next++
				return durable.EntryDraft{Kind: "message", Model: []ai.Message{system(ai.PromptSection{Name: "s", Value: new(fmt.Sprintf("v%d", next-1))})}}
			case pick < 10:
				seen["note"]++
				next++
				return durable.EntryDraft{Kind: "note", Data: map[string]any{"n": float64(next - 1)}}
			}
			var target *durable.EntryRecord
			if len(written) > 0 {
				target = &written[random(len(written))]
			}
			if pick < 12 && target != nil {
				edit := durable.ContextEdit{Target: target.Id, Action: durable.EditOmit}
				if random(2) == 0 {
					seen["omit"]++
				} else {
					seen["replace"]++
					edit = durable.ContextEdit{Target: target.Id, Action: durable.EditReplace, Messages: []ai.Message{user(fmt.Sprintf("r%d", next))}}
					next++
				}
				return durable.EntryDraft{Kind: "edit", Edits: []durable.ContextEdit{edit}}
			}
			if pick < 13 && target != nil {
				seen["head"]++
				next++
				return durable.EntryDraft{Kind: "summary", Head: new(target.Id), Model: []ai.Message{user(fmt.Sprintf("h%d", next-1))}}
			}
			seen["user"]++
			next++
			return durable.EntryDraft{Kind: "message", Model: []ai.Message{user(fmt.Sprintf("u%d", next-1))}}
		}
		appendTo := func(conversationId durable.ConversationId) error {
			return runtime.Commit(ctx, func(tx durable.Tx, _ stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				for range 1 + random(3) {
					entry, err := tx.AppendEntry(conversationId, draft())
					if err != nil {
						return nil, err
					}
					written = append(written, entry)
				}
				return nil, nil
			})
		}
		whole := func(conversation Conversation, options *durable.ContextOptions) durable.ContextView {
			view, err := conversation.Context(ctx, options)
			if err != nil {
				panic(err)
			}
			return view
		}
		read := func(conversationId durable.ConversationId, options *durable.ContextOptions) (durable.ContextView, error) {
			return runtime.Context(ctx, conversationId, options)
		}
		root := runtime.ConversationId()
		rootHandle := rootConversation
		expectSame := func(step string, got, want durable.ContextView) {
			if failure == nil && !reflect.DeepEqual(got, want) {
				failure = fmt.Errorf("%s: the extended read differs from the whole read\n got %d entries, %d messages\nwant %d entries, %d messages", step, len(got.Entries), len(got.Messages), len(want.Entries), len(want.Messages))
			}
		}
		for step := range 150 {
			if err := appendTo(root); err != nil {
				return err
			}
			got, err := read(root, nil)
			if err != nil {
				return err
			}
			expectSame(fmt.Sprint("step ", step), got, whole(rootHandle, nil))
			if step%10 == 9 {
				// Overlapping reads, one with an earlier cutoff, share the kept range.
				at := written[random(len(written))].Id
				var wholeView, cutView durable.ContextView
				var wholeErr, cutErr error
				var group sync.WaitGroup
				group.Add(2)
				go func() { defer group.Done(); wholeView, wholeErr = read(root, nil) }()
				go func() { defer group.Done(); cutView, cutErr = read(root, &durable.ContextOptions{At: &at}) }()
				group.Wait()
				if wholeErr != nil || cutErr != nil {
					return fmt.Errorf("overlapping reads: %w", errors.Join(wholeErr, cutErr))
				}
				expectSame(fmt.Sprint("step ", step, " whole"), wholeView, whole(rootHandle, nil))
				expectSame(fmt.Sprint("step ", step, " cut at ", at), cutView, whole(rootHandle, &durable.ContextOptions{At: &at}))
			}
		}
		// A fork sees its parent's entries through the fork point and extends with its own.
		fork, err := rootHandle.Fork(ctx, written[random(len(written))].Id, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		if err != nil {
			return err
		}
		for step := range 20 {
			got, err := read(fork.Id(), nil)
			if err != nil {
				return err
			}
			expectSame(fmt.Sprint("fork step ", step), got, whole(fork, nil))
			if err := appendTo(fork.Id()); err != nil {
				return err
			}
		}
		return tkComplete(ctx, runtime)
	})
	opened := tkOpenRoot(t, []durable.AnyTask{steps}, tkOptions{storage: store})
	rootConversation = opened.root
	opened.harness.Resume()
	id := tkStart(t, opened.root, steps)
	tkWaitOutcome(t, opened.harness, id)
	if failure != nil {
		t.Fatal(failure)
	}
	for _, category := range []string{"user", "assistant", "aborted", "error", "deferred", "result", "stray result", "system", "note", "omit", "replace", "head"} {
		if seen[category] == 0 {
			t.Errorf("the transcript never included a %s entry", category)
		}
	}
	mustClose(t, opened.harness)
}
