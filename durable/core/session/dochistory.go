package session

import "github.com/MichaelKinsy/PiG/durable/core/internal/delta"

// DocRevision names one value of a cached document incarnation: the incarnation (Epoch, never 0) and the number of changes adopted
// to it so far (Adopted). A host that kept the value of a revision asks for what changed since instead of the whole document.
type DocRevision struct {
	Epoch   uint64
	Adopted int
}

// maxDocHistory is how many adopted changes of a document the Session keeps for a host to catch up with; a host further behind
// gets the whole document.
const maxDocHistory = 256

// docHistory is the operations of the changes adopted to a document incarnation since a host first read it through a
// transaction, oldest first: batches[i] takes revision from+i to from+i+1.
type docHistory struct {
	recording bool
	from      int
	batches   [][]delta.Op
}

// start has the history record from revision adopted on, unless it already does.
func (h *docHistory) start(adopted int) {
	if !h.recording {
		h.recording, h.from = true, adopted
	}
}

// add records the change that took revision before to the next one.
func (h *docHistory) add(before int, ops []delta.Op) {
	if !h.recording {
		return
	}
	if before != h.from+len(h.batches) {
		// A change the history did not see: what it has no longer reaches the present.
		*h = docHistory{}
		return
	}
	h.batches = append(h.batches, ops)
	if len(h.batches) >= 2*maxDocHistory {
		h.from += maxDocHistory
		h.batches = append(h.batches[:0:0], h.batches[maxDocHistory:]...)
	}
}

// since is the operations that take revision have to revision now, and whether the history holds them.
func (h *docHistory) since(have, now int) ([]delta.Op, bool) {
	if !h.recording || have < h.from || have > now || now > h.from+len(h.batches) {
		return nil, false
	}
	var ops []delta.Op
	for _, batch := range h.batches[have-h.from : now-h.from] {
		ops = append(ops, batch...)
	}
	return ops, true
}

// adopt makes a prepared change the document's latest revision, recording its operations for hosts that catch up.
func (l *loadedDoc) adopt(prepared *delta.Prepared) error {
	before := l.tracker.Revision()
	if err := l.tracker.Adopt(prepared); err != nil {
		return err
	}
	l.history.add(before, prepared.Ops())
	return nil
}
