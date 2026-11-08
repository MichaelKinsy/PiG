package subprocess

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"weak"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// renderKeys names the transcript messages and entries the renderers draw for. Pi hands a renderer the same message object on every rebuild, so an
// extension keeps per-message state in a WeakMap; here the transcript's [extension.RenderIdentity] is that object, and the key the wire carries
// stands for it in the extension's process. A key lives as long as its identity: when the transcript drops the message the identity is collected and
// the extension is told to drop the state it kept (NotifyRenderRelease), as tool cards are (toolRenderSessions).
// pig additive (D276)
var renderKeys renderKeyRegistry

var renderKeySeq atomic.Uint64

type renderKeyRegistry struct {
	mu   sync.Mutex
	keys map[renderKeyOwner]string
}

// renderKeyOwner is the identity an extension process knows by a key: the same identity rendered through another connection (an extension that
// restarted) is a new message to the new process.
type renderKeyOwner struct {
	identity weak.Pointer[extension.RenderIdentity]
	conn     *Conn
}

type renderKeyRelease struct {
	owner renderKeyOwner
	key   string
}

// key returns the key of identity on conn, minting it on first use. It is empty without an identity or a connection: the renderer call is not for a
// displayed message, so the extension has no state to keep and no mouse to take.
func (r *renderKeyRegistry) key(conn *Conn, identity *extension.RenderIdentity) string {
	if identity == nil || conn == nil {
		return ""
	}
	owner := renderKeyOwner{identity: weak.Make(identity), conn: conn}
	r.mu.Lock()
	defer r.mu.Unlock()
	if key, ok := r.keys[owner]; ok {
		return key
	}
	key := fmt.Sprintf("render-%d", renderKeySeq.Add(1))
	if r.keys == nil {
		r.keys = make(map[renderKeyOwner]string)
	}
	r.keys[owner] = key
	runtime.AddCleanup(identity, r.release, renderKeyRelease{owner: owner, key: key})
	return key
}

// release forgets the key of a collected identity and tells its extension to drop what it kept.
func (r *renderKeyRegistry) release(released renderKeyRelease) {
	r.mu.Lock()
	delete(r.keys, released.owner)
	r.mu.Unlock()
	args, err := json.Marshal(RenderReleasePayload{Key: released.key})
	if err != nil {
		return
	}
	_ = released.owner.conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyRenderRelease, Args: args}})
}
