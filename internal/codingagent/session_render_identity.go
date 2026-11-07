package codingagent

import (
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// entryRenderIdentities hands out the [extension.RenderIdentity] of each session entry an extension renders. Pi's session manager holds one object
// per entry, and every rebuild of the transcript (compaction, /tree, the resume paint) renders that same object, so an entry renderer that keeps its
// state in a WeakMap keeps it across the rebuilds. PiG decodes an entry from its record on each rebuild, so the entry's id stands for the object here
// and the identity is looked up by it: one per entry id, for as long as the session holds the entry.
//
// The registry belongs to the [Session], the analogue of the session manager. Its log is replaced as a whole (replaceSessionLog: a session switch,
// a new session, a branch into a new session), and Pi builds new entry objects for each of those (setSessionFile parses the file again,
// createBranchedSession copies the path), so the replacement drops every identity; the extension is then told to release the state it kept for
// each, when the identity is collected (the transcript drops its components on the rebuild that follows). The registry holds at most one 16-byte
// identity per custom entry an extension rendered, none for an entry that has no renderer, and none per rebuild.
//
// pig additive (D276)
type entryRenderIdentities struct {
	mu  sync.Mutex
	ids map[string]*extension.RenderIdentity
}

// identity returns the identity of the entry with this id, making it on first use.
func (r *entryRenderIdentities) identity(id string) *extension.RenderIdentity {
	r.mu.Lock()
	defer r.mu.Unlock()
	if identity, ok := r.ids[id]; ok {
		return identity
	}
	if r.ids == nil {
		r.ids = make(map[string]*extension.RenderIdentity)
	}
	identity := extension.NewRenderIdentity()
	r.ids[id] = identity
	return identity
}

// clear forgets every identity.
func (r *entryRenderIdentities) clear() {
	r.mu.Lock()
	r.ids = nil
	r.mu.Unlock()
}

// EntryRenderIdentity is the identity an extension's entry renderer is handed for the entry with this id: the same one each time the entry is
// rendered, for as long as the Session holds the entry (see [entryRenderIdentities]).
func (s *Session) EntryRenderIdentity(id string) *extension.RenderIdentity {
	return s.renderIdentities.identity(id)
}
