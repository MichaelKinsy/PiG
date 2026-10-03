package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var definitionImports = map[string]string{"d": "github.com/MichaelKinsy/PiG/durable"}

// Ports packages/durable/test/session-definitions.test.ts
//
// Upstream's overload checks are TypeScript type assertions (expectTypeOf and @ts-expect-error). Go methods take erased
// tokens, so the positive overloads are compile-time assignments below, the owner-argument negatives that Go can
// only report at run time are asserted as Session errors, and the negatives that only the token's scope type rejects
// are run as Pi 1.0.0's runtime handles the same arguments (ignored; see the equivalence case).

type definitionState struct {
	Value float64 `json:"value"`
}

var (
	definitionSessionDoc = durable.DefineDoc(durable.DocDefinition[definitionState]{
		CommonDocDefinition: durable.CommonDocDefinition[definitionState]{Kind: "t.session", Version: 1},
		DocumentSemantics:   sessionScope,
		Initial:             func() definitionState { return definitionState{} },
	})
	definitionLatestDoc = durable.DefineDoc(durable.DocDefinition[definitionState]{
		CommonDocDefinition: durable.CommonDocDefinition[definitionState]{Kind: "t.latest", Version: 1},
		DocumentSemantics:   latestScope(durable.ForkCurrent),
		Initial:             func() definitionState { return definitionState{} },
	})
	definitionRewindableDoc = durable.DefineDoc(durable.DocDefinition[definitionState]{
		CommonDocDefinition: durable.CommonDocDefinition[definitionState]{Kind: "t.rewindable", Version: 1},
		DocumentSemantics:   rewindableScope(durable.ForkAsOf),
		Initial:             func() definitionState { return definitionState{} },
	})
	definitionTaskDoc = durable.DefineDoc(durable.DocDefinition[definitionState]{
		CommonDocDefinition: durable.CommonDocDefinition[definitionState]{Kind: "t.task", Version: 1},
		DocumentSemantics:   taskScope,
		Initial:             func() definitionState { return definitionState{} },
	})
	definitionSessionFamily = durable.DefineDocFamily(durable.DocFamilyDefinition[definitionState, float64]{
		CommonDocDefinition: durable.CommonDocDefinition[definitionState]{Kind: "t.session-family", Version: 1},
		DocumentSemantics:   sessionScope,
		Initial:             func(seed float64) definitionState { return definitionState{Value: seed} },
	})
	definitionConversationFamily = durable.DefineDocFamily(durable.DocFamilyDefinition[definitionState, float64]{
		CommonDocDefinition: durable.CommonDocDefinition[definitionState]{Kind: "t.conversation-family", Version: 1},
		DocumentSemantics:   rewindableScope(durable.ForkInitial),
		Initial:             func(seed float64) definitionState { return definitionState{Value: seed} },
	})
	definitionTaskFamily = durable.DefineDocFamily(durable.DocFamilyDefinition[definitionState, float64]{
		CommonDocDefinition: durable.CommonDocDefinition[definitionState]{Kind: "t.task-family", Version: 1},
		DocumentSemantics:   taskScope,
		Initial:             func(seed float64) definitionState { return definitionState{Value: seed} },
	})
)

// keep asserts a call's result type at compile time.
func keep[T any](value T, _ error) T { return value }

// Every owner, key, and seed overload compiles with its typed result.
var (
	_ = func(tx durable.Tx, conversationId durable.ConversationId, taskId durable.TaskId) {
		_ = keep[durable.Draft[definitionState]](durable.TxDoc(tx, definitionSessionDoc))
		_ = keep[durable.Draft[definitionState]](durable.TxDoc(tx, definitionLatestDoc, conversationId))
		_ = keep[durable.Draft[definitionState]](durable.TxDoc(tx, definitionRewindableDoc, conversationId))
		_ = keep[durable.Draft[definitionState]](durable.TxDoc(tx, definitionTaskDoc, taskId))
		_ = keep[durable.Draft[definitionState]](durable.TxDoc(tx, definitionSessionFamily, "k", 1.0))
		_ = keep[durable.Draft[definitionState]](durable.TxDoc(tx, definitionConversationFamily, conversationId, "k", 1.0))
		_ = keep[durable.Draft[definitionState]](durable.TxDoc(tx, definitionTaskFamily, taskId, "k", 1.0))
		_ = durable.TxRetireDoc(tx, definitionSessionFamily, "k")
	}
	_ = func(kernel *session.SessionImpl, tx durable.Tx, conversationId durable.ConversationId, at durable.EntryId) {
		_ = keep[*definitionState](durable.Snapshot(ctx, kernel, definitionLatestDoc, conversationId))
		_ = keep[*definitionState](durable.SnapshotAsOf(ctx, kernel, definitionRewindableDoc, at, conversationId))
		var state *chord.AttachedReplicatedState[obj]
		state, _ = kernel.DocumentStateErased(ctx, definitionSessionDoc)
		_ = state
		var watch durable.WatchHandle[obj]
		watch, _ = kernel.WatchDocErased(ctx, definitionSessionDoc)
		_ = watch
		note := durable.DefineEntry[struct {
			Text string `json:"text"`
		}]("t.note")
		var typed *durable.TypedEntry[struct {
			Text string `json:"text"`
		}]
		typed, _ = durable.TxEntry(tx, note, at)
		_ = typed
		var raw *delta.Object
		raw, _ = tx.Doc(definitionSessionDoc)
		_ = raw
	}
)

func TestDocumentDefinitions(t *testing.T) {
	t.Run("validates persisted version semantics", func(t *testing.T) {
		expectPanic(t, "positive integer", func() {
			durable.DefineDoc(durable.DocDefinition[obj]{CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: "k", Version: 0}, DocumentSemantics: sessionScope})
		})
		// Upstream also rejects version 1.5 at run time; a Go int version cannot hold a fraction, so the compiler does.
		testenv.ExpectTypeError(t, "a fractional version", definitionImports, `_ = d.CommonDocDefinition[struct{}]{Kind: "k", Version: 1.5}`)
		empty := durable.DefineDoc(durable.DocDefinition[obj]{CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: "", Version: 1}, DocumentSemantics: sessionScope})
		if empty.AnyDefinition().Kind != "" || definitionSessionDoc.AnyDefinition().Kind != "t.session" {
			t.Fatal("kinds are kept as defined")
		}
	})

	t.Run("types every owner, key, and seed overload", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		var taskId durable.TaskId
		commit(t, harness.Session, func(tx durable.Tx) error {
			var err error
			taskId, err = durable.CreateTask(tx, workTask, obj{"path": "a"}, conversationOwned(conversationId))
			must(t, err)
			for _, access := range []struct {
				token durable.AnyDocToken
				args  []any
			}{
				{definitionSessionDoc, nil},
				{definitionLatestDoc, []any{conversationId}},
				{definitionRewindableDoc, []any{conversationId}},
				{definitionTaskDoc, []any{taskId}},
				{definitionSessionFamily, []any{"k", 1}},
				{definitionConversationFamily, []any{conversationId, "k", 1}},
				{definitionTaskFamily, []any{taskId, "k", 1}},
			} {
				if _, err := tx.Doc(access.token, access.args...); err != nil {
					return err
				}
			}
			return nil
		})
		value, err := durable.Snapshot(ctx, harness.Session, definitionSessionFamily, "k")
		must(t, err)
		if value == nil || value.Value != 1 {
			t.Fatal("the typed snapshot decodes the seeded member")
		}
		// A conversation document without its owner is a TypeError upstream at compile time and a Session error here.
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(definitionLatestDoc)
			return err
		})
		expectErrorContains(t, err, "requires a conversation ID")
		must(t, harness.Session.Close(ctx))
	})

	t.Run("types historical reads, states, watches, and typed entries", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		note := durable.DefineEntry[struct {
			Text string `json:"text"`
		}]("t.note")
		var at durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			entry, err := durable.TxAppendEntry(tx, note, conversationId, durable.TypedEntryDraft[struct {
				Text string `json:"text"`
			}]{Data: struct {
				Text string `json:"text"`
			}{Text: "x"}})
			must(t, err)
			at = entry.Id
			if entry.TypedData.Text != "x" || entry.Kind != "t.note" {
				t.Fatal("the token supplies the kind and types the data")
			}
			if _, err = tx.Doc(definitionLatestDoc, conversationId); err != nil {
				return err
			}
			_, err = tx.Doc(definitionRewindableDoc, conversationId)
			return err
		})
		// A marker entry carries no data, and a raw append returns the record.
		marker := durable.DefineEntry[durable.Never]("t.marker")
		var rawId durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			typed, err := durable.TxAppendEntry(tx, marker, conversationId, durable.TypedEntryDraft[durable.Never]{})
			must(t, err)
			if typed.Kind != "t.marker" || typed.Data != nil {
				t.Fatalf("marker = %+v", typed.EntryRecord)
			}
			var raw durable.EntryRecord
			raw, err = tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "t.raw"})
			rawId = raw.Id
			return err
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			found, err := tx.Entry(rawId)
			must(t, err)
			if found == nil || found.Kind != "t.raw" {
				t.Fatalf("raw entry = %+v", found)
			}
			return nil
		})
		// The typed entry forms Go rejects at compile time, upstream's @ts-expect-error cases: data is typed by the
		// token, the token supplies the kind, and an entry kind without data takes none. A data entry's required data is
		// a zero value in Go, and a missing one has no compile-time form.
		const entrySnippet = `type noteData struct{ Text string }
var tx d.Tx
note := d.DefineEntry[noteData]("t.note")
marker := d.DefineEntry[d.Never]("t.marker")
`
		testenv.ExpectCompiles(t, "typed entries", definitionImports, entrySnippet+`
_, _ = d.TxAppendEntry(tx, note, 1, d.TypedEntryDraft[noteData]{Data: noteData{Text: "x"}})
_, _ = d.TxAppendEntry(tx, marker, 1, d.TypedEntryDraft[d.Never]{})`)
		// Each draft below is well-typed on its own, so only TxAppendEntry's binding of the draft to the token rejects it.
		testenv.ExpectTypeError(t, "data is typed by the token", definitionImports, entrySnippet+`
type numberNote struct{ Text int }
_, _ = d.TxAppendEntry(tx, note, 1, d.TypedEntryDraft[numberNote]{Data: numberNote{Text: 1}})`)
		testenv.ExpectTypeError(t, "the token supplies the kind", definitionImports, entrySnippet+`
_, _ = d.TxAppendEntry(tx, note, 1, d.TypedEntryDraft[noteData]{Kind: "t.note", Data: noteData{Text: "x"}})`)
		testenv.ExpectTypeError(t, "an entry kind without data takes none", definitionImports, entrySnippet+`
_, _ = d.TxAppendEntry(tx, marker, 1, d.TypedEntryDraft[int]{Data: 1})`)
		value, err := durable.SnapshotAsOf(ctx, harness.Session, definitionRewindableDoc, at, conversationId)
		must(t, err)
		if value == nil || value.Value != 0 {
			t.Fatal("the historical typed read")
		}
		// Session and task documents keep no history: a TypeError upstream at compile time and a Session error at run
		// time, as are latest conversation documents.
		_, err = harness.Session.SnapshotAsOfErased(ctx, definitionSessionDoc, at)
		expectErrorContains(t, err, "requires a conversation document")
		_, err = harness.Session.SnapshotAsOfErased(ctx, definitionTaskDoc, at, durable.TaskId(1))
		expectErrorContains(t, err, "requires a conversation document")
		_, err = harness.Session.SnapshotAsOfErased(ctx, definitionLatestDoc, at, conversationId)
		expectErrorContains(t, err, "does not retain historical content")
	})

	// The @ts-expect-error negatives of :77 and :140 that rest on a document token's scope and family-ness being part of
	// its type. Upstream's runtime (resolveAddress) reads only the arguments its scope needs, so on the same inputs Pi
	// 1.0.0 neither throws nor changes the address: the extra argument is ignored. Go's erased tokens take variadic
	// arguments through the same resolver and give Pi's results. A missing family seed is the `undefined` argument that
	// Pi's copyJson rejects (transaction.ts doc, chord json.ts copy), and Go's missing variadic argument is rejected the
	// same way. A seed of another type is stored by Pi and rejected by Go's typed Initial.
	t.Run("ignores the owner and seed arguments the token's scope does not take, as Pi's runtime does", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		taskId := durable.TaskId(0)
		commit(t, harness.Session, func(tx durable.Tx) error {
			var err error
			taskId, err = durable.CreateTask(tx, workTask, obj{"path": "a"}, conversationOwned(conversationId))
			return err
		})

		// tx.doc(SessionDoc, conversationId): the owner is not read; the address is the Session document's.
		commit(t, harness.Session, func(tx durable.Tx) error {
			withOwner, err := tx.Doc(definitionSessionDoc, conversationId)
			must(t, err)
			plain, err := tx.Doc(definitionSessionDoc)
			must(t, err)
			if withOwner != plain {
				t.Fatal("a Session document with an owner argument resolves to the same draft as without it")
			}
			return withOwner.Set("value", 7)
		})
		value, err := durable.Snapshot(ctx, harness.Session, definitionSessionDoc)
		must(t, err)
		if value == nil || value.Value != 7 {
			t.Fatalf("session document = %+v, want value 7", value)
		}

		// session.watchDoc(SessionDoc, conversationId, context) and documentState: the owner is not read.
		watch, err := harness.Session.WatchDocErased(ctx, definitionSessionDoc, conversationId)
		must(t, err)
		if watch == nil || watch.Value()["value"] != float64(7) {
			t.Fatalf("watch of a Session document with an owner argument = %v", watch)
		}
		_, _ = watch.Stop()
		state, err := harness.Session.DocumentStateErased(ctx, definitionSessionDoc, conversationId)
		must(t, err)
		if state == nil || state.Value()["value"] != float64(7) {
			t.Fatalf("state of a Session document with an owner argument = %v", state)
		}
		state.Dispose()

		// tx.doc(SessionFamily, "k"): no seed is `undefined` upstream, which copyJson rejects; nothing is created.
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(definitionSessionFamily, "k")
			return err
		})
		expectErrorContains(t, err, "Value contains a non-JSON undefined; expected strict JSON")
		unseeded, err := durable.Snapshot(ctx, harness.Session, definitionSessionFamily, "k")
		must(t, err)
		if unseeded != nil {
			t.Fatalf("a family access without a seed leaves no document: %+v", unseeded)
		}

		// tx.doc(SessionFamily, "s", "seed"): upstream stores {value: "seed"}; Go's typed seed rejects it and creates nothing.
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(definitionSessionFamily, "s", "seed")
			return err
		})
		expectErrorContains(t, err, "cannot unmarshal string")
		absent, err := durable.Snapshot(ctx, harness.Session, definitionSessionFamily, "s")
		must(t, err)
		if absent != nil {
			t.Fatalf("a rejected seed leaves no document: %+v", absent)
		}

		// session.snapshot(SessionFamily, "k", 1, context): the seed is not read.
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(definitionSessionFamily, "k", 2)
			return err
		})
		seeded, err := durable.Snapshot(ctx, harness.Session, definitionSessionFamily, "k", 1)
		must(t, err)
		if seeded == nil || seeded.Value != 2 {
			t.Fatalf("snapshot with a trailing seed = %+v, want the stored member {value: 2}", seeded)
		}

		// tx.retireDoc(TaskFamily, taskId, "k", 1): the seed is not read; the member is retired.
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(definitionTaskFamily, taskId, "k", 1)
			return err
		})
		commit(t, harness.Session, func(tx durable.Tx) error { return tx.RetireDoc(definitionTaskFamily, taskId, "k", 1) })
		retired, err := durable.Snapshot(ctx, harness.Session, definitionTaskFamily, taskId, "k")
		must(t, err)
		if retired != nil {
			t.Fatalf("a member retired with a trailing seed is gone: %+v", retired)
		}

		// tx.appendEntry(Note, conversationId, {}): upstream leaves `data` undefined; a Go draft with no data holds the
		// zero value of the data type, and the token still supplies the kind.
		commit(t, harness.Session, func(tx durable.Tx) error {
			note := durable.DefineEntry[struct {
				Text string `json:"text"`
			}]("t.note")
			entry, err := durable.TxAppendEntry(tx, note, conversationId, durable.TypedEntryDraft[struct {
				Text string `json:"text"`
			}]{})
			must(t, err)
			if entry.Kind != "t.note" || entry.TypedData.Text != "" {
				t.Fatalf("entry without data = %+v", entry.EntryRecord)
			}
			return nil
		})
		must(t, harness.Session.Close(ctx))
	})
}
