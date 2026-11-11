package views

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strconv"
	"sync"
	"time"

	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/views/room-store.ts (part 4): `RoomStore`'s factory: presence from `host.references`, the methods the rooms'
// peers and the session's worker reach, and the routines that keep the room (apps/p2/docs/rooms.md sections 3 and 4).

const (
	// callTimeout is how long the agent's call waits for the leader's answer.
	callTimeout = 60 * time.Second
	// lagLimit is Chord's heartbeat close timeout: a live room's peer behind everyone else longer than this is disconnected.
	lagLimit   = 30 * time.Second
	roomLinger = 10 * time.Second
)

// roomStoreParts is what the rooms' facet gives each store.
type roomStoreParts struct {
	memory *roomMemory
	// dropTokens drops the unused tokens of one opening of a view.
	dropTokens func(session, view, opening string)
}

func (store *roomStore) bunch() string { return store.bunchID }

func (store *roomStore) settle(id string, result outcome) {
	store.mu.Lock()
	call := store.waiting[id]
	delete(store.waiting, id)
	store.mu.Unlock()
	if call != nil {
		select {
		case call.settled <- result:
		default:
		}
	}
}

// laggards are the present peers behind every other peer for longer than the limit.
func (store *roomStore) laggards(now int64) []string {
	peers, _ := store.presenceValue()
	var out []string
	for _, id := range peers {
		own := store.acks[id]
		if own == nil {
			continue
		}
		behind := false
		others := 0
		lowest := 0
		for _, other := range peers {
			if other == id {
				continue
			}
			seq := 0
			if known := store.acks[other]; known != nil {
				seq = known.seq
			}
			if others == 0 || seq < lowest {
				lowest = seq
			}
			others++
		}
		behind = others > 0 && own.seq < lowest
		switch {
		case !behind:
			own.behind = nil
		case own.behind == nil:
			since := now
			own.behind = &since
		case time.Duration(now-*own.behind)*time.Millisecond > lagLimit:
			out = append(out, id)
		}
	}
	return out
}

func objectsOf(value delta.JsonValue) map[string]*delta.JsonObject {
	out := map[string]*delta.JsonObject{}
	if object, ok := value.(*delta.JsonObject); ok {
		for key, each := range object.All() {
			out[key], _ = each.(*delta.JsonObject)
		}
	}
	return out
}

// serveRoomStore serves `RoomStore`.
func serveRoomStore(facet host.FacetHost, parts roomStoreParts) error {
	references := facet.References(contract.ViewRoom)
	linger := roomLinger
	return facet.Serve(RoomStore, host.LocalFactory(func(params *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		session, view, opening := text(params, "session"), text(params, "view"), text(params, "opening")
		store := &roomStore{
			self: self, session: session, view: view, opening: opening, memory: parts.memory, dropTokens: parts.dropTokens,
			server:   self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0)),
			models:   self.Get(modelscontract.ModelConfig, delta.NewJsonObject(0)),
			openings: self.Get(contract.ViewOpenings, delta.JsonObjectOf("session", session)),
			backing:  self.Get(contract.ViewBacking, delta.JsonObjectOf("session", session, "view", view, "opening", opening)),
			room:     self.State(roomDoc).(*documents.Singleton),
			log:      self.State(logDoc).(*documents.Singleton),
			keyed:    self.State(keyedDoc).(*documents.Singleton),
			blocks:   self.State(blockDoc).(*documents.Keyed),
			info:     self.State(infoDoc).(*documents.Singleton),
			offers:   self.State(offersDoc).(*documents.Singleton),
			acks:     map[string]*ack{},
			waiting:  map[string]*pendingCall{},
		}
		random := make([]byte, 6)
		if _, err := rand.Read(random); err != nil {
			return contracts.Implementation{}, err
		}
		store.bunchID = "s" + base64.RawURLEncoding.EncodeToString(random)
		bunches := 0
		store.ids = NewElementIDGenerator(func() string {
			bunches++
			return store.bunchID + "." + strconv.FormatInt(int64(bunches-1), 36)
		})
		store.presence = self.Derive([]any{references}, func(values []any) (delta.JsonValue, error) {
			list, _ := values[0].([]any)
			peers := []any{}
			seen := map[string]bool{}
			leader := ""
			for _, each := range list {
				principal, _ := objectOf(each).Value("principal").(*delta.JsonObject)
				if principal == nil {
					continue
				}
				who, err := p2runtime.FromChord(principal)
				frame, isView := who.(p2runtime.ViewPrincipal)
				// One frame may hold its room more than once (a reconnect, a re-render): one peer per link.
				if err != nil || !isView || frame.Session != session || frame.View != view || frame.Opening != opening || seen[frame.Link] {
					continue
				}
				seen[frame.Link] = true
				peers = append(peers, delta.JsonObjectOf("id", frame.Link, "name", frame.ForName, "role", frame.ForRole, "mode", string(frame.Mode)))
				if leader == "" && frame.Mode == p2runtime.ViewWrite {
					leader = frame.Link
				}
			}
			result := delta.JsonObjectOf("peers", peers)
			if leader == "" {
				result.Set("leader", nil)
			} else {
				result.Set("leader", leader)
			}
			return result, nil
		})
		unsaved := self.Derive([]any{store.info}, func(values []any) (delta.JsonValue, error) {
			return intOf(objectOf(values[0]), "textSeq") > intOf(objectOf(values[0]), "savedSeq"), nil
		})
		// This room's share of the budget: counted when it opens, given back when it ends.
		self.Open(func(context.Context) (any, error) {
			store.memory.add(intOf(store.singleton(store.info), "bytes"))
			return nil, nil
		}, func(any, host.NativeCloseReason, context.Context) error {
			store.memory.add(-intOf(store.singleton(store.info), "bytes"))
			return nil
		})
		store.declareRoutines(self, unsaved)
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"room": host.Expose(store.room), "presence": store.presence, "log": host.Expose(store.log), "keyed": host.Expose(store.keyed),
			},
			Methods: store.methods(),
		}}, nil
	}), host.ServeOptions{Linger: &linger})
}

// declareRoutines declares the routines that keep the room.
func (store *roomStore) declareRoutines(self host.Self, unsaved chord.Watchable[delta.JsonValue]) {
	// Presence: departed peers' keyed messages, offers and acknowledgements go; calls pinned to them end uncertain (or go to the next
	// leader if idempotent); the leader's term grows when the leader changes.
	self.On(store.presence, func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
		if call.Kind != chord.WatchCallValue {
			return nil
		}
		return host.AwaitingCtx(ctx, func() error { return store.presenceChanged(ctx, objectOf(call.Value)) })
	})
	// A closed (or closed and reopened) view: its frames are revoked and its unused tokens dropped.
	self.On(store.openings.Member("openings"), func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
		if call.Kind != chord.WatchCallValue || objectOf(call.Value).Value(store.view) == store.opening {
			return nil
		}
		return host.AwaitingCtx(ctx, func() error {
			store.dropTokens(store.session, store.view, store.opening)
			_, err := store.server.Call("revokeFrames", delta.JsonObjectOf("view", p2runtime.ViewKey(store.session, store.view, store.opening)), ctx)
			return err
		})
	})
	// The view's state changed: an outside change resets a log room or becomes a text room's edit. The revision last taken is the
	// room's own (a document): a reload's new attempt compares it at its first call, so a change made meanwhile is taken before
	// anything is saved over it.
	self.On(store.backing.Member("current"), func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
		if call.Kind != chord.WatchCallValue {
			return nil
		}
		return host.AwaitingCtx(ctx, func() error {
			current := objectOf(call.Value)
			revision := intOf(current, "revision")
			kind := store.kind()
			if kind == "" || kind == "live" || store.seenRevision() < 0 {
				if revision != store.seenRevision() {
					return store.markSeen(ctx, revision)
				}
				return nil
			}
			return store.queue.run(ctx, func() error { return store.takeOutside(ctx, current.Value("value"), revision) })
		})
	})
	// A text room's text not yet written (read from its documents: a reload's restart saves at once) is written a little after the
	// last edit, and while edits keep coming, at that pace.
	self.On(unsaved, func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
		if call.Kind != chord.WatchCallValue || call.Value != true {
			return nil
		}
		return host.AwaitingCtx(ctx, func() error {
			// The flag stays true while edits keep coming: write until the text caught up.
			for store.kind() == "text" {
				facts := store.singleton(store.info)
				if intOf(facts, "textSeq") <= intOf(facts, "savedSeq") {
					break
				}
				if err := host.Sleep(textWritePause*time.Millisecond, ctx); err != nil {
					return err
				}
				if err := store.queue.run(ctx, func() error { return store.writeText(ctx) }); err != nil {
					return err
				}
			}
			return nil
		})
	})
	// Calls of an earlier attempt have nobody waiting for them; a live room's peer that stays behind everyone else is disconnected:
	// it rejoins after the messages it missed.
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		return host.AwaitingCtx(ctx, func() error {
			calls, _ := store.singleton(store.room).Value("calls").([]any)
			stale := false
			store.mu.Lock()
			for _, each := range calls {
				id, _ := objectOf(each).Value("id").(string)
				stale = stale || store.waiting[id] == nil
			}
			store.mu.Unlock()
			if stale {
				if err := store.change(store.room, ctx, func(draft *delta.Object) error {
					kept := []any{}
					store.mu.Lock()
					for _, each := range draft.Array("calls").Snapshot() {
						if id, _ := objectOf(each).Value("id").(string); store.waiting[id] != nil {
							kept = append(kept, each)
						}
					}
					store.mu.Unlock()
					return draft.Set("calls", kept)
				}); err != nil {
					return err
				}
			}
			for {
				if err := host.Sleep(lagLimit/6, ctx); err != nil {
					return err
				}
				if store.kind() != "live" {
					continue
				}
				store.mu.Lock()
				late := store.laggards(time.Now().UnixMilli())
				for _, id := range late {
					delete(store.acks, id)
				}
				store.mu.Unlock()
				for _, id := range late {
					if _, err := store.server.Call("revokeFrames", delta.JsonObjectOf("link", id), ctx); err != nil {
						return err
					}
				}
				if err := store.trimLive(ctx); err != nil {
					return err
				}
			}
		})
	})
}

// presenceChanged is the presence routine's step.
func (store *roomStore) presenceChanged(ctx host.Ctx, value *delta.JsonObject) error {
	present := map[string]bool{}
	peers, _ := value.Value("peers").([]any)
	for _, each := range peers {
		id, _ := objectOf(each).Value("id").(string)
		present[id] = true
	}
	leader, _ := value.Value("leader").(string)
	store.mu.Lock()
	changed := leader != store.lastLeader
	store.lastLeader = leader
	store.mu.Unlock()
	if changed {
		if err := store.change(store.room, ctx, func(draft *delta.Object) error { return draft.Set("term", float64(draftInt(draft, "term")+1)) }); err != nil {
			return err
		}
	}
	store.mu.Lock()
	for id := range store.acks {
		if !present[id] {
			delete(store.acks, id)
		}
	}
	store.mu.Unlock()
	var gone []string
	for id := range objectsOf(store.singleton(store.keyed).Value("peers")) {
		if !present[id] {
			gone = append(gone, id)
		}
	}
	if len(gone) > 0 {
		err := store.charged(ctx, func(tx documents.Tx, spent *ledger) error {
			draft, err := tx.Draft(store.keyed)
			if err != nil {
				return err
			}
			account, err := tx.Draft(store.info)
			if err != nil {
				return err
			}
			keyedSize := draftInt(account, "keyed")
			for _, id := range gone {
				entries := draft.Object("peers").Object(id)
				for _, key := range entries.Keys() {
					entryData, _ := entries.Object(key).Get("data").(string)
					if err := store.charge(account, -(keptEntry(key, entryData) + entryCost), spent, 0); err != nil {
						return err
					}
					keyedSize -= encodedEntry(key, entryData)
				}
				keyedSize -= encodedPeer(id)
				draft.Object("peers").Delete(id)
			}
			return account.Set("keyed", float64(keyedSize))
		})
		if err != nil {
			return err
		}
	}
	var left []string
	for id := range objectsOf(store.singleton(store.offers).Value("peers")) {
		if !present[id] {
			left = append(left, id)
		}
	}
	if len(left) > 0 {
		if err := store.change(store.offers, ctx, func(draft *delta.Object) error {
			for _, id := range left {
				draft.Object("peers").Delete(id)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	store.mu.Lock()
	type given struct {
		id   string
		call *pendingCall
	}
	var departed []given
	for id, call := range store.waiting {
		if !present[call.peer] {
			departed = append(departed, given{id, call})
		}
	}
	store.mu.Unlock()
	for _, each := range departed {
		method := ""
		for _, call := range store.singleton(store.room).Value("calls").([]any) {
			if objectOf(call).Value("id") == each.id {
				method, _ = objectOf(call).Value("method").(string)
			}
		}
		offer := objectsOf(store.singleton(store.offers).Value("peers"))[leader]
		offered := false
		if offer != nil {
			methods, _ := offer.Value("methods").([]any)
			for _, listed := range methods {
				offered = offered || objectOf(listed).Value("name") == method
			}
		}
		if each.call.idempotent && leader != "" && method != "" && offered {
			store.mu.Lock()
			each.call.peer, each.call.offer = leader, intOf(offer, "n")
			store.mu.Unlock()
			if err := store.change(store.room, ctx, func(draft *delta.Object) error {
				calls := draft.Array("calls")
				for i, call := range calls.Snapshot() {
					if objectOf(call).Value("id") == each.id {
						return calls.Object(i).Set("peer", leader)
					}
				}
				return nil
			}); err != nil {
				return err
			}
			continue
		}
		// Given to a frame that left: it may have run, whether another frame is left or not.
		store.settle(each.id, outcome{err: uncertain()})
	}
	return store.trimLive(ctx)
}

var _ = sync.Mutex{}
var _ = chordctx.Background
var _ = data.CodeInvalid
