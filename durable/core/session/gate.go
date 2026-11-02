package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// NeedPublication is the error a read or a barrier returns when a key it touches has an unpublished writer (session.ts touch): the line
// job that returned it keeps the line while it waits, and runs again from its start once the part that writes the key is published
// or will never be (the job saw state that is stored by then, as Pi's continues after its `await gate`). At is the ticks the job had
// spent when it touched the key: Pi's callback ran them while it waited, so only the rest of its duration follows the publication.
type NeedPublication struct {
	Gate *Gate
	At   int
	// Extra is the ticks the continuation takes beyond the publication's own (hopcost.GateResume): the gate of a read through gatedStore is
	// followed by `.then(read)`, whose promise adopts the read's.
	Extra int
}

func (e *NeedPublication) Error() string { return "key has an unpublished writer" }

// IsNeedPublication reports whether err is, or wraps, a *NeedPublication.
func IsNeedPublication(err error) bool {
	if err == nil {
		return false
	}
	_, ok := errors.AsType[*NeedPublication](err)
	return ok
}

// needPublication is the error of a job that waits for gate.
func (s *Session) needPublication(g *Gate) error { return s.needPublicationAfter(g, 0) }

func (s *Session) needPublicationAfter(g *Gate, extra int) error {
	if s.loop != nil {
		s.loop.hit()
	}
	return &NeedPublication{Gate: g, At: s.touchAt, Extra: extra}
}

// waitFor is the wait of a line job for the part it admitted: nil once it is published (a Session without a Loop publishes it as it is
// admitted), the error that settled it when it never was, else a *NeedPublication that runs the job again after.
func (s *Session) waitFor(part *Part) error {
	if part.gate.opened {
		return part.err
	}
	return s.needPublication(&part.gate)
}

// Touch is session.ts touch: nil when key has no unpublished writer, else a *NeedPublication for its gate (for AllKeys, while anything
// is unpublished). A job's every read touches its key first, so it never observes state an earlier job admitted and the writer has
// not published.
func (s *Session) Touch(key string) error { return s.touch(key, 0) }

func (s *Session) touch(key string, extra int) error {
	var g *Gate
	if key == AllKeys {
		g = s.writer.Drained()
	} else {
		g = s.writer.Gate(key)
	}
	if g == nil {
		return nil
	}
	return s.needPublicationAfter(g, extra)
}

// touchRead is touch for a read of gatedStore: `gate.then(read)` adopts the read's promise (hopcost.GatedReadAdoption).
func (s *Session) touchRead(key string) error { return s.touch(key, hopcost.GatedReadAdoption) }

// TouchRead is the wait of a read through the Session's gated Storage (keys.ts #after) for a host that serves the read from its own mirror:
// nil when key has no unpublished writer, else the *NeedPublication that runs the job again after.
func (s *Session) TouchRead(key string) error { return s.touchRead(key) }

// TouchEntries is the wait of a read of a conversation's entries (keys.ts #afterEntries) for a host that serves it from its own mirror.
func (s *Session) TouchEntries(conversationID ID) error { return s.touchEntries(conversationID) }

// touchEntries waits for what an unpublished commit wrote of a conversation's entries, its appends and its creation, as reading
// them from the Store fails for a conversation it does not have yet.
func (s *Session) touchEntries(conversationID ID) error {
	if err := s.touchRead(ConversationKey(conversationID)); err != nil {
		return err
	}
	return s.touchRead(AppendKey(conversationID))
}

// gatedStore is the Store a job on the line reads through (keys.ts GatedStorage): every read first touches its key, which makes the job
// wait while the key has an unpublished writer, so the read sees what the line admitted before it. Reads that cannot be keyed touch
// AllKeys. Commit passes through; the line never commits through it.
type gatedStore struct {
	Store
	s *Session
}

func (g *gatedStore) Conversation(id ID) (*jv.Object, error) {
	if err := g.s.touchRead(ConversationKey(id)); err != nil {
		return nil, err
	}
	return g.Store.Conversation(id)
}

func (g *gatedStore) ScanConversations(q ConversationQuery, limit int, after ID) (Page, error) {
	if err := g.s.touchRead(AllKeys); err != nil {
		return Page{}, err
	}
	return g.Store.ScanConversations(q, limit, after)
}

func (g *gatedStore) Entry(id ID) (*jv.Object, Seq, error) {
	if err := g.s.touchRead(EntryKey(id)); err != nil {
		return nil, 0, err
	}
	return g.Store.Entry(id)
}

func (g *gatedStore) EntryIn(conversationID, id ID) (*jv.Object, Seq, error) {
	// Visibility follows the conversation's immutable ancestry; the conversation may be unpublished itself.
	if err := g.s.touchRead(ConversationKey(conversationID)); err != nil {
		return nil, 0, err
	}
	if err := g.s.touchRead(EntryKey(id)); err != nil {
		return nil, 0, err
	}
	return g.Store.EntryIn(conversationID, id)
}

func (g *gatedStore) LatestHeadMarker(conversationID, atOrBefore ID) (*jv.Object, error) {
	if err := g.s.touchEntries(conversationID); err != nil {
		return nil, err
	}
	return g.Store.LatestHeadMarker(conversationID, atOrBefore)
}

func (g *gatedStore) ScanEntries(q EntryQuery, limit int, after ID) (Page, error) {
	if err := g.s.touchEntries(q.ConversationID); err != nil {
		return Page{}, err
	}
	return g.Store.ScanEntries(q, limit, after)
}

func (g *gatedStore) Task(id ID) (*jv.Object, error) {
	if err := g.s.touchRead(TaskKey(id)); err != nil {
		return nil, err
	}
	return g.Store.Task(id)
}

func (g *gatedStore) ScanTasks(q TaskQuery, limit int, after ID) (Page, error) {
	if err := g.s.touchRead(AllKeys); err != nil {
		return Page{}, err
	}
	return g.Store.ScanTasks(q, limit, after)
}

func (g *gatedStore) Submission(id ID) (*jv.Object, error) {
	if err := g.s.touchRead(SubmissionKey(id)); err != nil {
		return nil, err
	}
	return g.Store.Submission(id)
}

func (g *gatedStore) ScanSubmissions(q SubmissionQuery, limit int, after ID) (Page, error) {
	if err := g.s.touchRead(AllKeys); err != nil {
		return Page{}, err
	}
	return g.Store.ScanSubmissions(q, limit, after)
}

func (g *gatedStore) SubmissionByRequest(conversationID ID, requestID string) (*jv.Object, error) {
	if err := g.s.touchRead(RequestKey(conversationID, requestID)); err != nil {
		return nil, err
	}
	return g.Store.SubmissionByRequest(conversationID, requestID)
}

func (g *gatedStore) FindDocument(a Address, at Point) (*jv.Object, error) {
	if err := g.s.touchRead(DocumentKey(addressID(a))); err != nil {
		return nil, err
	}
	return g.Store.FindDocument(a, at)
}

func (g *gatedStore) Document(id ID, at Point) (*StoredDoc, error) {
	if err := g.s.touchRead(IncarnationKey(id)); err != nil {
		return nil, err
	}
	return g.Store.Document(id, at)
}

func (g *gatedStore) ScanDocuments(scope Scope, at Point, kind string, limit int, after ID) (Page, error) {
	if err := g.s.touchRead(ScopeKey(scope)); err != nil {
		return Page{}, err
	}
	return g.Store.ScanDocuments(scope, at, kind, limit, after)
}
