package views

import (
	"context"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports apps/p2/plugins/views/room-store.ts (part 5): the methods of `RoomStore` (apps/p2/docs/rooms.md sections 3 and 4).

func invalidArgs(message string) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeInvalid, Reason: data.ReasonArgs, Message: message}, nil)
}

func outgoingOf(value delta.JsonValue) []outgoing {
	list, _ := value.([]any)
	items := make([]outgoing, len(list))
	for i, each := range list {
		message := objectOf(each)
		items[i].data, _ = message.Value("data").(string)
		if key, ok := message.Value("key").(string); ok {
			items[i].key = &key
		}
	}
	return items
}

func (store *roomStore) setAck(peer string, seq int) {
	store.mu.Lock()
	known := store.acks[peer]
	if known == nil {
		known = &ack{}
		store.acks[peer] = known
	}
	known.seq = seq
	store.mu.Unlock()
}

// methods are the members the rooms' peers and the session's worker reach (suspended: each waits for documents and calls).
func (store *roomStore) methods() map[string]contracts.MethodFunction {
	return map[string]contracts.MethodFunction{
		"join": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			input := objectOf(args)
			peer, _ := input.Value("peer").(string)
			writer, _ := input.Value("writer").(bool)
			kind, hasKind := input.Value("kind").(string)
			current := store.kind()
			if hasKind && current != kind {
				// Changing the kind starts the room over: only a frame that may write, and only when no other frame uses the room.
				if !writer {
					name := current
					if name == "" {
						name = "new"
					}
					return nil, p2runtime.Forbidden("This view's room is a " + name + " room; this frame may only watch")
				}
				if current != "" {
					peers, _ := store.presenceValue()
					for _, id := range peers {
						if id != peer {
							return nil, invalidArgs("This view's room is a " + current + " room while frames use it")
						}
					}
				}
				if err := store.queue.run(ctx, func() error { return store.resetRoom(ctx, kind) }); err != nil {
					return nil, err
				}
			}
			seq := intOf(store.singleton(store.room), "seq")
			store.setAck(peer, seq)
			return delta.JsonObjectOf("from", float64(seq)), nil
		},
		"send": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			input := objectOf(args)
			peer, _ := input.Value("peer").(string)
			if err := store.publish(ctx, peer, outgoingOf(input.Value("messages"))); err != nil {
				return nil, err
			}
			if seq, ok := input.Value("ack").(float64); ok {
				store.setAck(peer, int(seq))
			}
			return nil, store.trimLive(ctx)
		},
		"ack": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			input := objectOf(args)
			peer, _ := input.Value("peer").(string)
			store.setAck(peer, min(intOf(input, "seq"), intOf(store.singleton(store.room), "seq")))
			return nil, store.trimLive(ctx)
		},
		"history": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			input := objectOf(args)
			messages, more, _, err := store.journalAfter(ctx, intOf(input, "after"), intOf(input, "limit"), intOf(store.singleton(store.room), "seq"))
			if err != nil {
				return nil, err
			}
			if messages == nil {
				messages = []any{}
			}
			return delta.JsonObjectOf("messages", messages, "more", more), nil
		},
		"offer": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			input := objectOf(args)
			peer, _ := input.Value("peer").(string)
			n := 1
			if current := objectsOf(store.singleton(store.offers).Value("peers"))[peer]; current != nil {
				n = intOf(current, "n") + 1
			}
			if err := store.change(store.offers, ctx, func(draft *delta.Object) error {
				return draft.Object("peers").Set(peer, delta.JsonObjectOf("n", float64(n), "methods", input.Value("methods")))
			}); err != nil {
				return nil, err
			}
			// Calls given to this peer under its earlier offer may or may not run now.
			store.mu.Lock()
			var stale []string
			for id, call := range store.waiting {
				if call.peer == peer && call.offer < n {
					stale = append(stale, id)
				}
			}
			store.mu.Unlock()
			for _, id := range stale {
				store.settle(id, outcome{err: uncertain()})
			}
			return nil, nil
		},
		"answer": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
			input := objectOf(args)
			peer, _ := input.Value("peer").(string)
			answer := objectOf(input.Value("answer"))
			id, _ := answer.Value("id").(string)
			store.mu.Lock()
			call := store.waiting[id]
			store.mu.Unlock()
			if call == nil || call.peer != peer {
				return nil, p2runtime.Forbidden("this call is not this frame's")
			}
			if message, failed := answer.Value("error").(string); failed {
				store.settle(id, outcome{err: data.NewPluginError("method_failed", message)})
			} else {
				store.settle(id, outcome{result: answer.Value("result")})
			}
			return nil, nil
		},
		"snapshot": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			input := objectOf(args)
			peer, _ := input.Value("peer").(string)
			snapshot := objectOf(input.Value("snapshot"))
			if store.kind() != "log" {
				return nil, invalidArgs("Only log rooms take snapshots")
			}
			// Checked in the serialized step, against the base as it is then: a snapshot overtaken by a newer one (or by an outside
			// change) never moves the base back.
			fits := func() bool {
				facts := store.singleton(store.room)
				base, _ := facts.Value("base").(*delta.JsonObject)
				_, leader := store.presenceValue()
				return leader == peer && base != nil && intOf(snapshot, "epoch") == intOf(base, "epoch") &&
					intOf(snapshot, "term") == intOf(facts, "term") && intOf(snapshot, "seq") > intOf(base, "seq") &&
					intOf(snapshot, "seq") <= intOf(facts, "seq")
			}
			written := false
			err := store.queue.run(ctx, func() error {
				if !fits() {
					return nil
				}
				// The snapshot's cost (its base, less the old base and the journal it replaces) is reserved before the view's state is
				// written: nothing persists that the room cannot take too.
				old := 0
				if base, ok := store.singleton(store.room).Value("base").(*delta.JsonObject); ok {
					old = 2 * jsonLen(base.Value("value"))
				}
				_, _, freed, err := store.journalAfter(ctx, intOf(store.singleton(store.info), "from"), int(^uint(0)>>1), intOf(snapshot, "seq"))
				if err != nil {
					return err
				}
				reserved := max(0, 2*jsonLen(snapshot.Value("value"))-old-freed)
				if store.memory.get()+reserved > store.budget() {
					return roomFull()
				}
				store.memory.add(reserved)
				defer store.memory.add(-reserved)
				done, err := store.backing.Call("write", delta.JsonObjectOf("revision", float64(store.seenRevision()), "value", snapshot.Value("value")), ctx)
				if err != nil {
					return err
				}
				if done != true {
					outside, number := store.backed()
					return store.takeOutside(ctx, outside, number)
				}
				if err := store.markSeen(ctx, store.seenRevision()+1); err != nil {
					return err
				}
				if !fits() {
					return nil
				}
				if err := store.setBase(ctx, &baseSpec{seq: intOf(snapshot, "seq"), epoch: intOf(snapshot, "epoch"), value: snapshot.Value("value")}, reserved); err != nil {
					return err
				}
				written = true
				return nil
			})
			if err != nil {
				return nil, err
			}
			if !written {
				return nil, invalidArgs("The snapshot is stale")
			}
			return nil, nil
		},
		"edit": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			input := objectOf(args)
			editor, _ := input.Value("editor").(string)
			batch := objectOf(input.Value("batch"))
			if store.kind() != "text" {
				return nil, invalidArgs("Only text rooms take edits")
			}
			edits, err := editsOf(batch.Value("edits"))
			if err != nil {
				return nil, err
			}
			return nil, store.queue.run(ctx, func() error { return store.logEdits(ctx, editor, intOf(batch, "n"), edits) })
		},
		"methods": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
			_, leader := store.presenceValue()
			if leader == "" {
				return nil, notOpen()
			}
			if offer := objectsOf(store.singleton(store.offers).Value("peers"))[leader]; offer != nil {
				if methods, ok := offer.Value("methods").([]any); ok {
					return append([]any{}, methods...), nil
				}
			}
			return []any{}, nil
		},
		"call": store.call,
	}
}

// call runs a method in the leader's frame, pinned to it and to its offer number; it ends with the answer, the timeout or the
// caller's cancellation, whichever comes first, and then leaves the room's calls in the store's own scope.
func (store *roomStore) call(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
	input := objectOf(args)
	method, _ := input.Value("method").(string)
	_, leader := store.presenceValue()
	if leader == "" {
		return nil, notOpen()
	}
	offer := objectsOf(store.singleton(store.offers).Value("peers"))[leader]
	var offered *delta.JsonObject
	if offer != nil {
		methods, _ := offer.Value("methods").([]any)
		for _, listed := range methods {
			if objectOf(listed).Value("name") == method {
				offered = objectOf(listed)
			}
		}
	}
	if offered == nil {
		return nil, invalidArgs("The view offers no method \"" + method + "\"")
	}
	id := newUUID()
	idempotent, _ := offered.Value("idempotent").(bool)
	settled := make(chan outcome, 1)
	store.mu.Lock()
	store.waiting[id] = &pendingCall{peer: leader, offer: intOf(offer, "n"), idempotent: idempotent, settled: settled}
	store.mu.Unlock()
	timer := time.AfterFunc(callTimeout, func() {
		store.settle(id, outcome{err: data.NewPluginError("timeout", "The view did not answer within a minute")})
	})
	defer func() {
		timer.Stop()
		store.mu.Lock()
		delete(store.waiting, id)
		store.mu.Unlock()
		// Nobody waits for the answer any more: the goroutine below ends.
		select {
		case settled <- outcome{err: ctx.Err()}:
		default:
		}
		calls, _ := store.singleton(store.room).Value("calls").([]any)
		for _, each := range calls {
			if objectOf(each).Value("id") == id {
				_ = store.change(store.room, chordctx.WithoutAbortSignal(ctx), func(draft *delta.Object) error {
					kept := []any{}
					for _, call := range draft.Array("calls").Snapshot() {
						if objectOf(call).Value("id") != id {
							kept = append(kept, call)
						}
					}
					return draft.Set("calls", kept)
				})
				break
			}
		}
	}()
	if err := store.change(store.room, ctx, func(draft *delta.Object) error {
		_, err := draft.Array("calls").Push(delta.JsonObjectOf("id", id, "method", method, "args", input.Value("args"), "peer", leader))
		return err
	}); err != nil {
		return nil, err
	}
	// Await parks this handler with the host; the channel gets the answer, or the timeout's error (a cancelled caller's wait ends by
	// itself).
	waited := make(chan chordctx.Settled[outcome], 1)
	go func() {
		result := <-settled
		waited <- chordctx.Settled[outcome]{Value: result}
	}()
	result, err := chordctx.Await(ctx, waited)
	if err != nil {
		return nil, err
	}
	return result.result, result.err
}
