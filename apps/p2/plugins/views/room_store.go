package views

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/views/room-store.ts (part 2): the store's accounting and its numbering, publishing and trimming of
// messages. Every change of the room's documents is one transaction of the store; what it charges to the memory budget is taken
// back when it fails.

// ledger is what one transaction charged, to take back when it fails.
type ledger struct{ bytes int }

// outgoing is a message a peer sends, or the room logs for an editor: an optional key, its data as JSON text, and for a text
// room's batch of edits its number.
type outgoing struct {
	key  *string
	data string
	n    *int
}

// pendingCall is a call of the agent waiting for its frame's answer.
type pendingCall struct {
	peer       string
	offer      int
	idempotent bool
	settled    chan outcome
}

type ack struct {
	seq    int
	behind *int64
}

// textState is a text room's document and its editors' watermarks, as this attempt holds them.
type textState struct {
	doc   *TextDoc
	marks TextMarks
}

// roomStore is one room's store in one attempt of its object.
type roomStore struct {
	self                           host.Self
	session, view, opening         string
	memory                         *roomMemory
	dropTokens                     func(session, view, opening string)
	server, models, backing        *host.Reference
	openings                       *host.Reference
	room, log, keyed, info, offers *documents.Singleton
	blocks                         *documents.Keyed
	presence                       chord.Watchable[delta.JsonValue]
	queue                          serialQueue

	// What this attempt keeps outside documents (a reload's restart starts them over).
	mu         sync.Mutex
	acks       map[string]*ack
	waiting    map[string]*pendingCall
	text       *textState
	ids        *ElementIDGenerator
	lastLeader string
	bunchID    string
}

func (store *roomStore) singleton(handle *documents.Singleton) *delta.JsonObject {
	value, _ := handle.Value()
	return value
}

// budget is what all rooms may keep, in bytes.
func (store *roomStore) budget() int {
	mebibytes := defaultMemoryMiB
	if snapshot, err := store.models.Member("snapshot").Value(); err == nil {
		if settings, ok := objectOf(snapshot).Value("settings").(*delta.JsonObject); ok {
			if configured, ok := settings.Value("viewRoomsMemory").(float64); ok {
				mebibytes = int(configured)
			}
		}
	}
	return mebibytes * 1024 * 1024
}

// charge charges bytes to the room inside a transaction, before anything it pays for is kept: refused past the budget. Checked
// and counted in one step, so a concurrent charge (another room's) sees it; charged takes it back when the transaction fails.
func (store *roomStore) charge(account *delta.Object, bytes int, ledger *ledger, reserved int) error {
	if bytes > 0 && store.memory.get()-reserved+bytes > store.budget() {
		return roomFull()
	}
	if err := account.Set("bytes", float64(draftInt(account, "bytes")+bytes)); err != nil {
		return err
	}
	ledger.bytes += bytes
	store.memory.add(bytes)
	return nil
}

// charged runs fn as one transaction of this store; its charges are taken back when it fails.
func (store *roomStore) charged(ctx context.Context, fn func(tx documents.Tx, ledger *ledger) error) error {
	spent := &ledger{}
	_, err := store.self.Tx(func(tx documents.Tx) (any, error) { return nil, fn(tx, spent) }, ctx)
	if err != nil {
		store.memory.add(-spent.bytes)
	}
	return err
}

func (store *roomStore) change(handle *documents.Singleton, ctx context.Context, fn func(draft *delta.Object) error) error {
	_, err := handle.Change(ctx, func(draft *delta.Object) (any, error) { return nil, fn(draft) })
	return err
}

// presenceOf is who is in the room now, and the leader.
func (store *roomStore) presenceValue() (peers []string, leader string) {
	object := objectOf(store.presence.Value())
	list, _ := object.Value("peers").([]any)
	for _, each := range list {
		id, _ := objectOf(each).Value("id").(string)
		peers = append(peers, id)
	}
	leader, _ = object.Value("leader").(string)
	return peers, leader
}

func (store *roomStore) peerIDs() map[string]bool {
	present := map[string]bool{}
	peers, _ := store.presenceValue()
	for _, id := range peers {
		present[id] = true
	}
	return present
}

func (store *roomStore) kind() string {
	kind, _ := store.singleton(store.room).Value("kind").(string)
	return kind
}

// publish numbers items of from and publishes them in one transaction (numbers, journal and window agree whatever else runs): keyed
// ones set their entry's leaves, unkeyed ones go to the journal and the window, which is trimmed to its bounds. Charged before
// anything is kept; a keyed member past the link part limit is refused.
func (store *roomStore) publish(ctx context.Context, from string, items []outgoing) error {
	return store.charged(ctx, func(tx documents.Tx, spent *ledger) error {
		numbers, err := tx.Draft(store.room)
		if err != nil {
			return err
		}
		account, err := tx.Draft(store.info)
		if err != nil {
			return err
		}
		window, err := tx.Draft(store.log)
		if err != nil {
			return err
		}
		var byKey *delta.Object
		for _, item := range items {
			if item.key != nil {
				if byKey, err = tx.Draft(store.keyed); err != nil {
					return err
				}
				break
			}
		}
		keyedSize := draftInt(account, "keyed")
		for _, item := range items {
			seq := draftInt(numbers, "seq") + 1
			if err := numbers.Set("seq", float64(seq)); err != nil {
				return err
			}
			if item.key == nil || byKey == nil {
				if err := store.charge(account, 2*(jsLen(item.data)+jsLen(from))+entryCost, spent, 0); err != nil {
					return err
				}
				message := func() *delta.JsonObject {
					object := delta.JsonObjectOf("seq", float64(seq), "from", from, "data", item.data)
					if item.n != nil {
						object.Set("n", float64(*item.n))
					}
					return object
				}
				block, err := tx.Draft(store.blocks, blockOf(seq))
				if err != nil {
					return err
				}
				if _, err := block.Array("messages").Push(message()); err != nil {
					return err
				}
				if _, err := window.Array("messages").Push(message()); err != nil {
					return err
				}
				if item.n != nil {
					if err := account.Set("textSeq", float64(seq)); err != nil {
						return err
					}
				}
				continue
			}
			peers := byKey.Object("peers")
			entries := peers.Object(from)
			if entries == nil {
				if err := peers.Set(from, delta.NewJsonObject(0)); err != nil {
					return err
				}
				entries = peers.Object(from)
			}
			entry := entries.Object(*item.key)
			previous := ""
			if entry != nil {
				previous, _ = entry.Get("data").(string)
			}
			var before *string
			if entry != nil {
				before = &previous
			}
			value := contract.Tagged(item.data, before)
			// A peer's first key adds its own entry (its ID) to the member.
			first := entry == nil && len(entries.Keys()) == 0
			keyedSize += encodedEntry(*item.key, value)
			if entry != nil {
				keyedSize -= encodedEntry(*item.key, previous)
			}
			if first {
				keyedSize += encodedPeer(from)
			}
			if keyedRoot+keyedSize > maxClientFrame {
				return data.NewPluginError("too_large", "The room's keyed messages would not fit one snapshot for a joining frame (the link's part limit)")
			}
			cost := keptEntry(*item.key, value)
			if entry != nil {
				cost -= keptEntry(*item.key, previous)
			} else {
				cost += entryCost
			}
			if err := store.charge(account, cost, spent, 0); err != nil {
				return err
			}
			// The entry stays and only its two primitive leaves change: a slow peer's pending sets fold.
			if entry != nil {
				if err := entry.Set("seq", float64(seq)); err != nil {
					return err
				}
				if err := entry.Set("data", value); err != nil {
					return err
				}
			} else if err := entries.Set(*item.key, delta.JsonObjectOf("seq", float64(seq), "data", value)); err != nil {
				return err
			}
		}
		if err := account.Set("keyed", float64(keyedSize)); err != nil {
			return err
		}
		messages := window.Array("messages").Snapshot()
		bytes := 0
		for _, message := range messages {
			text, _ := objectOf(message).Value("data").(string)
			bytes += 2 * jsLen(text)
		}
		drop := 0
		for len(messages)-drop > 1 && (len(messages)-drop > windowMessages || bytes > windowBytes) {
			text, _ := objectOf(messages[drop]).Value("data").(string)
			bytes -= 2 * jsLen(text)
			drop++
		}
		if drop > 0 {
			if err := window.Set("start", float64(intOf(objectOf(messages[drop-1]), "seq"))); err != nil {
				return err
			}
			if _, err := window.Array("messages").Splice(0, drop); err != nil {
				return err
			}
		}
		return nil
	})
}

// maxClientFrame is the link's part limit (`MAX_CLIENT_FRAME`).
const maxClientFrame = hosts.MaxClientFrame

// trimTo drops journal messages up to seq (whole blocks retired, the last one cut) and from the window. What it drops and frees is
// read inside the transaction, on the store's line: concurrent trims never count a block twice, and the journal's start never
// moves back.
func (store *roomStore) trimTo(ctx context.Context, seq int) error {
	if seq <= intOf(store.singleton(store.info), "from") {
		return nil
	}
	return store.charged(ctx, func(tx documents.Tx, spent *ledger) error { return store.trimIn(tx, spent, seq) })
}

// trimIn is trimTo's step inside a transaction.
func (store *roomStore) trimIn(tx documents.Tx, spent *ledger, seq int) error {
	account, err := tx.Draft(store.info)
	if err != nil {
		return err
	}
	if seq <= draftInt(account, "from") {
		return nil
	}
	last := seq / blockSize
	freed := 0
	for block := (draftInt(account, "from") + 1) / blockSize; block <= last; block++ {
		draft, err := tx.Draft(store.blocks, itoa(block))
		if err != nil {
			return err
		}
		messages := draft.Array("messages").Snapshot()
		cut := len(messages)
		if block >= last {
			cut = -1
			for i, message := range messages {
				if intOf(objectOf(message), "seq") > seq {
					cut = i
					break
				}
			}
			if cut < 0 {
				cut = len(messages)
			}
		}
		removed, err := draft.Array("messages").Splice(0, cut)
		if err != nil {
			return err
		}
		for _, message := range removed {
			freed += messageCost(objectOf(message))
		}
		if block < last {
			if err := tx.Retire(store.blocks, itoa(block)); err != nil {
				return err
			}
		}
	}
	if err := store.charge(account, -freed, spent, 0); err != nil {
		return err
	}
	if err := account.Set("from", float64(seq)); err != nil {
		return err
	}
	window, err := tx.Draft(store.log)
	if err != nil {
		return err
	}
	messages := window.Array("messages").Snapshot()
	at := len(messages)
	for i, message := range messages {
		if intOf(objectOf(message), "seq") > seq {
			at = i
			break
		}
	}
	if _, err := window.Array("messages").Splice(0, at); err != nil {
		return err
	}
	if draftInt(window, "start") < seq {
		return window.Set("start", float64(seq))
	}
	return nil
}

// trimLive: a live room drops what every present peer applied.
func (store *roomStore) trimLive(ctx context.Context) error {
	facts := store.singleton(store.room)
	if kind, _ := facts.Value("kind").(string); kind != "live" {
		return nil
	}
	low := intOf(facts, "seq")
	store.mu.Lock()
	for id := range store.peerIDs() {
		if known := store.acks[id]; known != nil {
			low = min(low, known.seq)
		}
	}
	store.mu.Unlock()
	return store.trimTo(ctx, low)
}

// baseSpec is a log or text room's base.
type baseSpec struct {
	seq, epoch int
	value      delta.JsonValue
}

// setBase sets the base (log and text rooms; nil: none) as of message seq, charged by its size, and drops the journal up to it.
func (store *roomStore) setBase(ctx context.Context, base *baseSpec, reserved int) error {
	size := 0
	if base != nil {
		size = 2 * jsonLen(base.value)
	}
	// One transaction: the journal up to the base goes with the base's arrival (a joiner never sees one without the other), and what
	// it frees pays for the base first.
	return store.charged(ctx, func(tx documents.Tx, spent *ledger) error {
		if base != nil {
			if err := store.trimIn(tx, spent, base.seq); err != nil {
				return err
			}
		}
		facts, err := tx.Draft(store.room)
		if err != nil {
			return err
		}
		old := 0
		if current, ok := facts.Snapshot().Value("base").(*delta.JsonObject); ok {
			old = 2 * jsonLen(current.Value("value"))
		}
		account, err := tx.Draft(store.info)
		if err != nil {
			return err
		}
		if err := store.charge(account, size-old, spent, reserved); err != nil {
			return err
		}
		if base == nil {
			return facts.Set("base", nil)
		}
		return facts.Set("base", delta.JsonObjectOf("seq", float64(base.seq), "epoch", float64(base.epoch), "value", base.value))
	})
}

// rebase is a new base of value as of the room's newest message.
func (store *roomStore) rebase(ctx context.Context, value delta.JsonValue, epoch int) error {
	return store.setBase(ctx, &baseSpec{seq: intOf(store.singleton(store.room), "seq"), epoch: epoch, value: value}, 0)
}

// journalAfter is the journal's messages after after (and up to upTo), block by block, at most limit and about a window's bytes;
// cost is what they are charged.
func (store *roomStore) journalAfter(ctx context.Context, after, limit, upTo int) (messages []any, more bool, cost int, err error) {
	bytes := 0
	from := intOf(store.singleton(store.info), "from")
	for block := max(after+1, from+1) / blockSize; block <= upTo/blockSize; block++ {
		stored, err := store.blocks.Get(ctx, itoa(block))
		if err != nil {
			return nil, false, 0, err
		}
		list, _ := stored.Value("messages").([]any)
		for _, each := range list {
			message := objectOf(each)
			seq := intOf(message, "seq")
			if seq <= after || seq > upTo {
				continue
			}
			text, _ := message.Value("data").(string)
			if len(messages) >= limit || (len(messages) > 0 && bytes+2*jsLen(text) > windowBytes) {
				return messages, true, cost, nil
			}
			bytes += 2 * jsLen(text)
			cost += messageCost(message)
			messages = append(messages, message)
		}
	}
	return messages, false, cost, nil
}
