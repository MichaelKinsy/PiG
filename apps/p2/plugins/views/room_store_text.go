package views

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// Ports apps/p2/plugins/views/room-store.ts (part 3): a text room's text, applied here and written to the worker (apps/p2/docs/rooms.md
// section 3, "Text").

// seenRevision is the view state's revision this room took last (`info.seen`).
func (store *roomStore) seenRevision() int { return intOf(store.singleton(store.info), "seen") }

func (store *roomStore) markSeen(ctx context.Context, revision int) error {
	return store.change(store.info, ctx, func(draft *delta.Object) error { return draft.Set("seen", float64(revision)) })
}

// backed is the view's state as its room sees it now.
func (store *roomStore) backed() (value delta.JsonValue, revision int) {
	current := objectOf(mustValue(store.backing.Member("current").Value()))
	return current.Value("value"), intOf(current, "revision")
}

func mustValue(value delta.JsonValue, _ error) delta.JsonValue { return value }

// textOf is a view state's text (none: empty).
func textOfState(value delta.JsonValue) string {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return ""
	}
	text, _ := object.Value("text").(string)
	return text
}

// stateObject is a view state's value as an object (another value: an empty one).
func stateObject(value delta.JsonValue) *delta.JsonObject {
	if object, ok := value.(*delta.JsonObject); ok {
		return object.Clone()
	}
	return delta.NewJsonObject(0)
}

func (store *roomStore) textState() *textState {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.text
}

func (store *roomStore) setText(state *textState) {
	store.mu.Lock()
	store.text = state
	store.mu.Unlock()
}

// textRoom is the text room's document as stored and as its base.
func textRoom(state *textState) delta.JsonValue { return SaveText(state.doc, state.marks) }

// loadedText is the text room's document: rebuilt from the base and the journal after it (a reload's restart), or loaded from the
// room's own document (checked) with the view's text taken in, or the view's text with fresh IDs.
func (store *roomStore) loadedText(ctx context.Context) (*textState, error) {
	if state := store.textState(); state != nil {
		return state, nil
	}
	facts := store.singleton(store.room)
	if base, ok := facts.Value("base").(*delta.JsonObject); ok {
		if doc, marks, ok := LoadText(base.Value("value")); ok {
			marks = cloneMarks(marks)
			for after, more := intOf(base, "seq"), true; more; {
				messages, hasMore, _, err := store.journalAfter(ctx, after, 1000, intOf(facts, "seq"))
				if err != nil {
					return nil, err
				}
				for _, each := range messages {
					message := objectOf(each)
					after = intOf(message, "seq")
					if !message.Has("n") {
						continue
					}
					data, _ := message.Value("data").(string)
					doc = ApplyEdits(doc, parseEdits(data))
					from, _ := message.Value("from").(string)
					if n := int64(intOf(message, "n")); n > marks[from] {
						marks[from] = n
					}
				}
				more = hasMore
			}
			state := &textState{doc: doc, marks: marks}
			store.setText(state)
			return state, nil
		}
	}
	current, _ := store.backed()
	wanted := textOfState(current)
	var state *textState
	if doc, marks, ok := LoadText(mustValue(store.backing.Member("room").Value())); !ok {
		state = &textState{doc: FreshText(wanted, store.bunch()), marks: TextMarks{}}
	} else if wanted == doc.String() || len(utf16Units(wanted)) > MaxText {
		// The view's text changed while no room was open (the agent, a page): taken as one edit over the stored document, so its IDs
		// (and pending edits of reconnecting frames) stay valid. Over the limit: not taken.
		state = &textState{doc: doc, marks: marks}
	} else {
		edits := EditsTo(doc, wanted, func(before *ElementID, count int64) ElementID { return store.ids.GenerateAfter(before, count) })
		next := cloneMarks(marks)
		next[serverEditor] = marks[serverEditor] + 1
		state = &textState{doc: ApplyEdits(doc, edits), marks: next}
	}
	store.setText(state)
	return state, nil
}

func cloneMarks(marks TextMarks) TextMarks {
	out := TextMarks{}
	for editor, mark := range marks {
		out[editor] = mark
	}
	return out
}

// writeText writes the text room's text into the view's state and its own document (serialized). When the state changed meanwhile,
// that change is taken first (its newer value is here already: an answer follows its state); the text is then still unsaved, and
// the save routine runs again.
func (store *roomStore) writeText(ctx context.Context) error {
	current, err := store.loadedText(ctx)
	if err != nil {
		return err
	}
	upTo := intOf(store.singleton(store.info), "textSeq")
	backed, _ := store.backed()
	value := stateObject(backed)
	value.Set("text", current.doc.String())
	revision := store.seenRevision()
	written, err := store.backing.Call("write", delta.JsonObjectOf("revision", float64(revision), "value", value, "room", textRoom(current)), ctx)
	if err != nil {
		return err
	}
	if written == true {
		return store.change(store.info, ctx, func(draft *delta.Object) error {
			if err := draft.Set("seen", float64(revision+1)); err != nil {
				return err
			}
			return draft.Set("savedSeq", float64(max(draftInt(draft, "savedSeq"), upTo)))
		})
	}
	outside, number := store.backed()
	return store.takeOutside(ctx, outside, number)
}

// takeOutside takes a change of the view's state the room did not make: the agent's or a page's.
func (store *roomStore) takeOutside(ctx context.Context, value delta.JsonValue, revision int) error {
	if revision == store.seenRevision() {
		return nil
	}
	if err := store.markSeen(ctx, revision); err != nil {
		return err
	}
	facts := store.singleton(store.room)
	switch store.kind() {
	case "log":
		epoch := 0
		if base, ok := facts.Value("base").(*delta.JsonObject); ok {
			epoch = intOf(base, "epoch")
		}
		return store.rebase(ctx, value, epoch+1)
	case "text":
	default:
		return nil
	}
	current, err := store.loadedText(ctx)
	if err != nil {
		return err
	}
	wanted := textOfState(value)
	if wanted == current.doc.String() {
		return nil
	}
	// Over the limit: the room's text stays and is written back over the outside value.
	if len(utf16Units(wanted)) > MaxText {
		return store.change(store.info, ctx, func(draft *delta.Object) error {
			seq := intOf(facts, "seq")
			if err := draft.Set("textSeq", float64(seq)); err != nil {
				return err
			}
			return draft.Set("savedSeq", float64(min(draftInt(draft, "savedSeq"), seq-1)))
		})
	}
	edits := EditsTo(current.doc, wanted, func(before *ElementID, count int64) ElementID { return store.ids.GenerateAfter(before, count) })
	// The server's own batches continue its watermark (kept in the room's document across restarts).
	return store.logEdits(ctx, serverEditor, int(current.marks[serverEditor])+1, edits)
}

// logEdits applies a batch of edits and logs it (empty when its number is at or below its editor's watermark).
func (store *roomStore) logEdits(ctx context.Context, editor string, n int, edits []TextEdit) error {
	current, err := store.loadedText(ctx)
	if err != nil {
		return err
	}
	fresh := int64(n) > current.marks[editor]
	logged := []TextEdit{}
	if fresh {
		logged = edits
	}
	// Published (and charged) first: a refused batch changes nothing.
	if err := store.publish(ctx, editor, []outgoing{{data: EditsJSON(logged), n: &n}}); err != nil {
		return err
	}
	if fresh {
		marks := cloneMarks(current.marks)
		marks[editor] = int64(n)
		store.setText(&textState{doc: ApplyEdits(current.doc, edits), marks: marks})
	}
	facts := store.singleton(store.room)
	baseSeq, epoch := 0, 0
	if base, ok := facts.Value("base").(*delta.JsonObject); ok {
		baseSeq, epoch = intOf(base, "seq"), intOf(base, "epoch")
	}
	if intOf(facts, "seq")-baseSeq >= textBaseEvery {
		state, err := store.loadedText(ctx)
		if err != nil {
			return err
		}
		return store.rebase(ctx, textRoom(state), epoch)
	}
	return nil
}

// resetRoom starts the room over as a room of kind.
func (store *roomStore) resetRoom(ctx context.Context, kind string) error {
	store.setText(nil)
	state, revision := store.backed()
	if err := store.markSeen(ctx, revision); err != nil {
		return err
	}
	epoch := 1
	if base, ok := store.singleton(store.room).Value("base").(*delta.JsonObject); ok {
		epoch = intOf(base, "epoch") + 1
	}
	if err := store.setBase(ctx, nil, 0); err != nil {
		return err
	}
	if err := store.change(store.room, ctx, func(draft *delta.Object) error { return draft.Set("kind", kind) }); err != nil {
		return err
	}
	if err := store.trimTo(ctx, intOf(store.singleton(store.room), "seq")); err != nil {
		return err
	}
	switch kind {
	case "log":
		return store.rebase(ctx, state, epoch)
	case "text":
		loaded, err := store.loadedText(ctx)
		if err != nil {
			return err
		}
		return store.rebase(ctx, textRoom(loaded), epoch)
	}
	return nil
}

var _ = documents.Tx(nil)
