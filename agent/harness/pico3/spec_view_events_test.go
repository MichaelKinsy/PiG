package pico3

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Source: packages/agent/test/harness/pico3/spec-view-events.test.ts.
type partialViewModels struct {
	*fakeModels
	gate *testGate
}

func (p partialViewModels) Stream(ctx context.Context, _ *ai.Model, _ RequestOptions) iter.Seq2[ai.AssistantMessageEvent, error] {
	return func(yield func(ai.AssistantMessageEvent, error) bool) {
		message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{}, API: "anthropic-messages", Provider: "anthropic", Model: "fake-1", Usage: fakeUsage(1, 1), StopReason: ai.StopReasonStop, Timestamp: 1}
		if !yield(ai.StartEvent{Partial: message}, nil) {
			return
		}
		message.Content = append(message.Content, ai.TextContent{})
		if !yield(ai.TextStartEvent{ContentIndex: 0, Partial: message}, nil) {
			return
		}
		message.Content[0] = ai.TextContent{Text: "partial"}
		if !yield(ai.TextDeltaEvent{ContentIndex: 0, Delta: "partial", Partial: message}, nil) {
			return
		}
		if err := p.gate.Wait(ctx); err != nil {
			yield(nil, err)
			return
		}
		if !yield(ai.TextEndEvent{ContentIndex: 0, Content: "partial", Partial: message}, nil) {
			return
		}
		yield(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}, nil)
	}
}
func viewEventTypes(envelope *Envelope) []string {
	var types []string
	for _, event := range envelope.Events {
		types = append(types, str(event, "type"))
	}
	return types
}
func viewHasEvent(envelope *Envelope, name string) bool {
	return slices.Contains(viewEventTypes(envelope), name)
}
func viewOp(op Op, kind, root string) bool {
	if len(op) < 2 {
		return false
	}
	path, _ := op[1].([]any)
	return op[0] == kind && len(path) > 0 && path[0] == root
}

func TestViewSnapshotExcludesRawPrivateState(t *testing.T) {
	env := openEnv(t, openOptions{})
	watch := must(env.root.Watch(bg))
	defer watch.Stop()
	allowed := []string{"compaction", "config", "conversation", "entries", "inbox", "plugins", "tasks", "turn"}
	for key := range watch.View {
		if !slices.Contains(allowed, key) {
			t.Fatalf("private view key %s", key)
		}
	}
	for _, key := range []string{"config", "inbox", "plugins"} {
		if _, ok := watch.View[key]; !ok {
			t.Fatalf("missing %s", key)
		}
	}
	for _, task := range asObject(watch.View["tasks"]) {
		for _, key := range []string{"checkpoint", "outcome", "memos"} {
			if _, ok := asObject(task)[key]; ok {
				t.Fatalf("private task key %s", key)
			}
		}
	}
}

func TestViewRevisionsIgnoreOtherConversations(t *testing.T) {
	env := openEnv(t, openOptions{})
	other := must(env.h.CreateConversation(bg, ConversationSpec{}, nil))
	collector := collectWatch(t, env.root)
	must(env.root.Write(bg, NewEntry{Kind: "root.one"}))
	must(other.Write(bg, NewEntry{Kind: "other.only"}))
	must(env.root.Write(bg, NewEntry{Kind: "root.two"}))
	var revisions []int
	for _, envelope := range collector.Envelopes() {
		revisions = append(revisions, envelope.Revision)
	}
	equal(t, revisions, []int{collector.watch.Revision() + 1, collector.watch.Revision() + 2}, "watch-local revisions")
}

func TestViewAdmissionIsOneAtomicEnvelope(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: func([]JsonObject, int) fakeResponse { return textResponse("answer") }, gate: gate})})
	collector := collectWatch(t, env.root)
	input := env.send(env.root, "hello")
	gate.Arrivals(t, 1)
	var admissions []*Envelope
	for _, envelope := range collector.Envelopes() {
		if viewHasEvent(envelope, "turn.started") {
			admissions = append(admissions, envelope)
		}
	}
	equal(t, len(admissions), 1, "atomic admission")
	admission := admissions[0]
	if !slices.ContainsFunc(admission.Ops, func(op Op) bool { return viewOp(op, "p", "entries") }) || !slices.ContainsFunc(admission.Ops, func(op Op) bool { return viewOp(op, "s", "turn") }) {
		t.Fatalf("admission ops: %v", admission.Ops)
	}
	equal(t, viewEventTypes(admission), []string{"input.placed", "entry.added", "turn.started"}, "admission event order")
	gate.Open()
	env.wait(input)
}

func TestViewTerminalAnswerSharesSettlementEnvelope(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: func([]JsonObject, int) fakeResponse { return textResponse("answer") }, gate: gate})})
	collector := collectWatch(t, env.root)
	input := env.send(env.root, "hello")
	gate.Arrivals(t, 1)
	gate.Open()
	result := env.wait(input)
	var terminal *Envelope
	for _, envelope := range collector.Envelopes() {
		if viewHasEvent(envelope, "turn.ended") {
			terminal = envelope
		}
	}
	if terminal == nil || !viewHasEvent(terminal, "generation.completed") {
		t.Fatal("completion envelope missing")
	}
	ended, added := false, false
	for _, event := range terminal.Events {
		if str(event, "type") == "turn.ended" {
			ended = str(event, "status") == "done" && Id(numberOr(event["answer"], 0)) == *result.Answer
		}
		if str(event, "type") == "entry.added" {
			added = Id(numberOr(asObject(event["entry"])["id"], 0)) == *result.Answer
		}
	}
	if !ended || !added || !slices.ContainsFunc(terminal.Ops, func(op Op) bool { return viewOp(op, "d", "turn") }) {
		t.Fatalf("terminal envelope %+v", terminal)
	}
}

func TestViewLateJoinerSeesPartialWithoutReplay(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: partialViewModels{fakeModels: newFake(fakeOptions{respond: echoScript}), gate: gate}, root: &RootSpec{Rewindable: JsonObject{"model": testModel}}})
	input := env.send(env.root, "stream")
	gate.Arrivals(t, 1)
	viewEventually(t, func() bool {
		watch := must(env.root.Watch(bg))
		defer watch.Stop()
		return viewStreaming(watch.View) == "partial"
	})
	collector := collectWatch(t, env.root)
	turn := asObject(collector.view["turn"])
	equal(t, asObject(turn["generation"])["stage"], "streaming", "late generation stage")
	content := arr(asObject(turn["message"]), "content")
	equal(t, asObject(content[0])["text"], "partial", "late partial text")
	equal(t, len(collector.Envelopes()), 0, "snapshot does not replay events")
	gate.Open()
	env.wait(input)
	if len(collector.Envelopes()) == 0 {
		t.Fatal("no subsequent events")
	}
}

func TestViewHeadCommitSplicesAndEmitsMatchingEvents(t *testing.T) {
	env := openEnv(t, openOptions{})
	must(env.root.Write(bg, NewEntry{Kind: "before"}))
	collector := collectWatch(t, env.root)
	check(t, env.root.Reset(bg, new("fresh")))
	envelopes := collector.Envelopes()
	equal(t, len(envelopes), 1, "one reset envelope")
	envelope := envelopes[0]
	var ops []Op
	for _, op := range envelope.Ops {
		if viewOp(op, "p", "entries") {
			ops = append(ops, op)
		}
	}
	equal(t, len(ops), 2, "truncate then insert")
	equal(t, ops[0][2], 0, "truncate starts at zero")
	if numberOr(ops[0][3], 0) <= 0 {
		t.Fatal("did not remove old range")
	}
	equal(t, viewEventTypes(envelope), []string{"head.moved", "entry.added"}, "head event order")
}

func TestViewProjectionFailurePreservesPersistedWriter(t *testing.T) {
	reports := make(chan error, 2)
	env := openEnv(t, openOptions{onReport: func(err error) { reports <- err }})
	namespace := must(env.h.Namespace("spec.bad-projection", NamespaceDefaults{Sticky: JsonObject{"fail": false}}, func(slice JsonObject) (JsonValue, error) {
		if slice["fail"] == true {
			return nil, errors.New("projection failed")
		}
		return JsonObject{"ok": true}, nil
	}))
	watch := must(env.root.Watch(bg))
	watch.Start(func(*Envelope) {})
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		node, err := tx.Plugins(namespace)
		if err != nil {
			return nil, err
		}
		node.Set("fail", true)
		return nil, nil
	})
	check(t, err)
	equal(t, watch.Closed(), true, "projection closes watch")
	select {
	case err := <-reports:
		if !strings.Contains(err.Error(), "projection failed") {
			t.Fatal(err)
		}
	default:
		t.Fatal("projection error missing")
	}
	equal(t, asObject(asObject(env.sticky()["plugins"])[namespace.Id])["fail"], true, "writer persisted")
}

func TestViewWatchersShareEnvelopeAndHaveIndependentLifetime(t *testing.T) {
	env := openEnv(t, openOptions{})
	first, second := must(env.root.Watch(bg)), must(env.root.Watch(bg))
	defer second.Stop()
	var a, b *Envelope
	first.Start(func(envelope *Envelope) { a = envelope })
	second.Start(func(envelope *Envelope) { b = envelope })
	must(env.root.Write(bg, NewEntry{Kind: "shared"}))
	if a == nil || a != b {
		t.Fatal("watchers did not receive same envelope object")
	}
	first.Stop()
	must(env.root.Write(bg, NewEntry{Kind: "second-only"}))
	equal(t, first.Closed(), true, "first closed")
	equal(t, second.Closed(), false, "second remains")
}

func TestViewPreStartOrderAndIdempotentBoundaries(t *testing.T) {
	env := openEnv(t, openOptions{})
	watch := must(env.root.Watch(bg))
	must(env.root.Write(bg, NewEntry{Kind: "buffered.one"}))
	must(env.root.Write(bg, NewEntry{Kind: "buffered.two"}))
	var received []*Envelope
	watch.Start(func(envelope *Envelope) { received = append(received, envelope) })
	watch.Start(func(*Envelope) { panic("second start must be ignored") })
	equal(t, len(received), 2, "prestart delivery")
	equal(t, received[1].Revision, received[0].Revision+1, "delivery order")
	watch.Stop()
	watch.Stop()
	must(env.root.Write(bg, NewEntry{Kind: "after.stop"}))
	equal(t, len(received), 2, "hard stop boundary")
}

func TestViewListenerFailureDoesNotAffectSiblingOrWriter(t *testing.T) {
	var mu sync.Mutex
	var reports []error
	env := openEnv(t, openOptions{onReport: func(err error) { mu.Lock(); reports = append(reports, err); mu.Unlock() }})
	broken, healthy := must(env.root.Watch(bg)), must(env.root.Watch(bg))
	defer healthy.Stop()
	events := 0
	broken.Start(func(*Envelope) { panic("listener failed") })
	healthy.Start(func(*Envelope) { events++ })
	id := must(env.root.Write(bg, NewEntry{Kind: "survives.listener"}))
	if id <= 0 {
		t.Fatal("writer not persisted")
	}
	equal(t, broken.Closed(), true, "broken closes")
	equal(t, healthy.Closed(), false, "healthy survives")
	equal(t, events, 1, "healthy event")
	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 1 || !strings.Contains(reports[0].Error(), "listener failed") {
		t.Fatalf("reports: %v", reports)
	}
}
