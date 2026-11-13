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
	mu sync.Mutex
	// keys holds the keys of each connection that has not ended, by identity: the same identity rendered through another connection (an
	// extension that restarted) is a new message to the new process. A connection's keys go when it ends (forget).
	keys map[*Conn]map[weak.Pointer[extension.RenderIdentity]]renderKey
}

// renderKey is a minted key and the cleanup that releases it when its identity is collected.
type renderKey struct {
	key     string
	cleanup runtime.Cleanup
}

type renderKeyRelease struct {
	identity weak.Pointer[extension.RenderIdentity]
	conn     *Conn
	key      string
}

// key returns the key of identity on conn, minting it on first use. It is empty without an identity or a connection: the renderer call is not for a
// displayed message, so the extension has no state to keep and no mouse to take. It is empty on a connection that has ended, too: its process
// keeps no state, and a key would hold the connection.
func (r *renderKeyRegistry) key(conn *Conn, identity *extension.RenderIdentity) string {
	if identity == nil || conn == nil {
		return ""
	}
	id := weak.Make(identity)
	r.mu.Lock()
	defer r.mu.Unlock()
	if held, ok := r.keys[conn][id]; ok {
		return held.key
	}
	// forget runs after the connection's Done closes, and under r.mu: a key minted before it is dropped by it, and none is minted after.
	select {
	case <-conn.Done():
		return ""
	default:
	}
	key := fmt.Sprintf("render-%d", renderKeySeq.Add(1))
	if r.keys == nil {
		r.keys = make(map[*Conn]map[weak.Pointer[extension.RenderIdentity]]renderKey)
	}
	if r.keys[conn] == nil {
		r.keys[conn] = make(map[weak.Pointer[extension.RenderIdentity]]renderKey)
	}
	r.keys[conn][id] = renderKey{key: key, cleanup: runtime.AddCleanup(identity, r.release, renderKeyRelease{identity: id, conn: conn, key: key})}
	return key
}

// forget drops the keys of a connection that has ended, and the cleanups that would release them. A message outlives the extension that
// draws it: a reload replaces the extension, and the transcript draws the same identity through the new connection. The keys of the ended
// one, and their cleanups, held that connection for as long as the transcript showed their messages, one connection for every reload. Its
// process is gone or runs a later generation, which has no state under those keys to drop.
func (r *renderKeyRegistry) forget(conn *Conn) {
	r.mu.Lock()
	keys := r.keys[conn]
	delete(r.keys, conn)
	r.mu.Unlock()
	for _, held := range keys {
		held.cleanup.Stop()
	}
}

// release forgets the key of a collected identity and tells its extension to drop what it kept.
func (r *renderKeyRegistry) release(released renderKeyRelease) {
	r.mu.Lock()
	if keys := r.keys[released.conn]; keys != nil {
		delete(keys, released.identity)
		if len(keys) == 0 {
			delete(r.keys, released.conn)
		}
	}
	r.mu.Unlock()
	args, err := json.Marshal(RenderReleasePayload{Key: released.key})
	if err != nil {
		return
	}
	_ = released.conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyRenderRelease, Args: args}})
}
