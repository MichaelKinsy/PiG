// Package viewstest is what tests of the views plugin share: a text room's client half (the frame's side, apps/p2/web/upstream/
// plugins/views/kit/text-client.ts, Pi's TypeScript run in the browser) ported to Go, so Go tests drive a room as a frame does.
package viewstest

import (
	"math/rand/v2"
	"strconv"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views"
)

// Batch is a batch of edits this editor sent and the server has not logged back yet.
type Batch struct {
	N     int
	Edits []views.TextEdit
}

// TextClient is the client state of one editor of a text room: the state the server confirmed, this editor's edits it has not
// confirmed yet, and the text shown, which is the confirmed state with the pending edits replayed on top.
type TextClient struct {
	editor    string
	ids       *views.ElementIDGenerator
	confirmed *views.TextDoc
	pending   []Batch
	current   *views.TextDoc
	n         int
}

// NewTextClient starts from doc (a base) as editor editor; n is its last batch number (it survives reconnects).
func NewTextClient(editor string, doc *views.TextDoc, n int) *TextClient {
	bunches := 0
	prefix := editor
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	prefix += strconv.FormatUint(rand.Uint64N(1<<62), 36)[:6]
	return &TextClient{
		editor: editor, confirmed: doc, current: doc, n: n,
		ids: views.NewElementIDGenerator(func() string {
			bunches++
			return prefix + "." + strconv.FormatInt(int64(bunches-1), 36)
		}),
	}
}

// Doc is the text shown: confirmed plus pending.
func (client *TextClient) Doc() *views.TextDoc { return client.current }

// Pending is the batches sent and not yet logged back.
func (client *TextClient) Pending() []Batch { return client.pending }

// N is the editor's last batch number.
func (client *TextClient) N() int { return client.n }

// Input is the person changing the text to next: the batch to send (nil: nothing changed).
func (client *TextClient) Input(next string) *Batch {
	edits := views.EditsTo(client.current, next, func(before *views.ElementID, count int64) views.ElementID {
		return client.ids.GenerateAfter(before, count)
	})
	if len(edits) == 0 {
		return nil
	}
	client.n++
	batch := Batch{N: client.n, Edits: edits}
	client.pending = append(client.pending, batch)
	client.current = views.ApplyEdits(client.current, edits)
	return &batch
}

func (client *TextClient) replay(from *views.TextDoc) *views.TextDoc {
	doc := from
	for _, batch := range client.pending {
		doc = views.ApplyEdits(doc, batch.Edits)
	}
	return doc
}

// Confirm is the server logging a batch of editor's (n): applied to the confirmed state; this editor's drop from pending.
func (client *TextClient) Confirm(editor string, n int, edits []views.TextEdit) {
	client.confirmed = views.ApplyEdits(client.confirmed, edits)
	if editor == client.editor {
		client.pending = client.after(n)
	}
	client.current = client.replay(client.confirmed)
}

// Reset starts over from a base (a joiner, a reset) that holds this editor's batches up to mark: those leave pending, the others
// are replayed on it and stay pending.
func (client *TextClient) Reset(doc *views.TextDoc, mark int) {
	client.confirmed = doc
	client.pending = client.after(mark)
	client.current = client.replay(doc)
}

func (client *TextClient) after(n int) []Batch {
	var kept []Batch
	for _, batch := range client.pending {
		if batch.N > n {
			kept = append(kept, batch)
		}
	}
	return kept
}
