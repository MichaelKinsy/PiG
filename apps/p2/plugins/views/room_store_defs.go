package views

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/views/room-store.ts (part 1): one room's store (apps/p2/docs/rooms.md section 3): the contract every
// `ViewRoom` of one opening of a custom view shares, its documents, and the accounting of what a room keeps.

func meth(sides ...schema.Schema) *contracts.Method {
	converted := make([]contracts.Schema, len(sides))
	for i, side := range sides {
		converted[i] = p2runtime.Contract(side)
	}
	return contracts.NewMethod(converted...)
}

func obj(properties schema.Properties) schema.Schema { return schema.Obj(properties) }

// RoomStore is one room's store (server-internal): every `ViewRoom` of one opening of a view shares it.
var RoomStore = must(contracts.Local("p2.view-room-store", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("session", contracts.Str()), contracts.Prop("view", contracts.Str()), contracts.Prop("opening", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("room", p2runtime.Contract(contract.RoomSchema)),
		contracts.Mem("presence", p2runtime.Contract(contract.PresenceSchema)),
		contracts.Mem("log", p2runtime.Contract(contract.LogSchema)),
		contracts.Mem("keyed", p2runtime.Contract(contract.KeyedByPeerSchema)),
		// `writer`: the frame may write; only a writer changes the room's kind (a reset).
		contracts.Mem("join", meth(obj(schema.Properties{schema.Prop("peer", schema.Str()), schema.Prop("writer", schema.Bool()), schema.Opt("kind", contract.RoomKindSchema)}),
			obj(schema.Properties{schema.Prop("from", schema.Int())}))),
		contracts.Mem("send", meth(obj(schema.Properties{schema.Prop("peer", schema.Str()), schema.Prop("messages", contract.OutgoingSchema), schema.Opt("ack", schema.Int())}))),
		contracts.Mem("ack", meth(obj(schema.Properties{schema.Prop("peer", schema.Str()), schema.Prop("seq", schema.Int())}))),
		contracts.Mem("history", meth(contract.HistoryArgsSchema, contract.HistorySchema)),
		contracts.Mem("offer", meth(obj(schema.Properties{schema.Prop("peer", schema.Str()), schema.Prop("methods", schema.Arr(contract.MethodInfoSchema, schema.MaxItems(100)))}))),
		contracts.Mem("answer", meth(obj(schema.Properties{schema.Prop("peer", schema.Str()), schema.Prop("answer", contract.AnswerSchema)}))),
		contracts.Mem("snapshot", meth(obj(schema.Properties{schema.Prop("peer", schema.Str()), schema.Prop("snapshot", contract.SnapshotSchema)}))),
		contracts.Mem("edit", meth(obj(schema.Properties{schema.Prop("editor", schema.Str()), schema.Prop("batch", contract.EditBatchSchema)}))),
		contracts.Mem("methods", contracts.NewMethod(contracts.Schema{}, p2runtime.Contract(schema.Arr(contract.MethodInfoSchema)))),
		contracts.Mem("call", meth(contract.CallArgsSchema, schema.Unknown())),
	},
}))

// roomMemory is what all rooms of the server keep, in bytes (settings.json `viewRoomsMemory`).
type roomMemory struct{ used atomic.Int64 }

func (memory *roomMemory) add(bytes int) { memory.used.Add(int64(bytes)) }
func (memory *roomMemory) get() int      { return int(memory.used.Load()) }

const (
	// windowMessages: the window every peer holds is at most this many journal messages…
	windowMessages = 256
	// windowBytes: …and this much of their data (UTF-16 bytes).
	windowBytes = 512 * 1024
	// blockSize is journal messages per block (one keyed document): an append drafts one block, never the whole journal.
	blockSize = 256
	// entryCost is what one retained entry costs beyond its strings, in bytes: measured on Node 24 for a journal message, 117 bytes;
	// charged for keyed entries and editors' marks too.
	entryCost = 128
	// keyedEncoding is the keyed member's encoding beyond its keys and data (`:{"seq":…,"data":},`), keyedPeer a peer's colon, braces and
	// comma, keyedRoot the root (`{"peers":{}}`).
	keyedEncoding = 33
	keyedPeer     = 4
	keyedRoot     = 12
	// textBaseEvery: a text room makes a new base after this many journal messages.
	textBaseEvery = 200
	// textWritePause is how long a text room waits after an edit before it writes the text.
	textWritePause = 500
	// serverEditor is the editor of the text the room takes from outside (the agent, a page's state).
	serverEditor = "server"
	// defaultMemoryMiB is the default of settings.json's `viewRoomsMemory`.
	defaultMemoryMiB = 64
)

func mustDoc(name string, shape schema.Schema, keyed bool, initial func() *delta.JsonObject) *documents.StateDefinition {
	definition, err := documents.DefineState(name, documents.StateOptions{Schema: p2runtime.Contract(shape), Version: 1, Keyed: keyed, Initial: initial})
	if err != nil {
		panic(err)
	}
	return definition
}

var (
	roomDoc = mustDoc("p2.view-room.room", contract.RoomSchema, false, func() *delta.JsonObject {
		// Each new store (not a reload's restart: its documents stay) is an incarnation of its own.
		return delta.JsonObjectOf("store", newUUID(), "kind", nil, "seq", 0.0, "term", 0.0, "base", nil, "calls", []any{})
	})
	logDoc   = mustDoc("p2.view-room.log", contract.LogSchema, false, func() *delta.JsonObject { return delta.JsonObjectOf("start", 0.0, "messages", []any{}) })
	keyedDoc = mustDoc("p2.view-room.keyed", contract.KeyedByPeerSchema, false, func() *delta.JsonObject { return delta.JsonObjectOf("peers", delta.NewJsonObject(0)) })
	// blockDoc: the journal's messages numbered in [key × blockSize, (key + 1) × blockSize), by block.
	blockDoc = mustDoc("p2.view-room.block", obj(schema.Properties{schema.Prop("messages", schema.Arr(contract.MessageSchema))}), true,
		func() *delta.JsonObject { return delta.JsonObjectOf("messages", []any{}) })
	// infoDoc is the room's accounting: the journal holds the messages after `from`; `bytes` is what the room keeps, `keyed` the keyed
	// member's encoded size; a text room's text changed at message `textSeq` and was written as of `savedSeq`; `seen` is the view
	// state's revision the room took last (its own writes included; -1: none), kept here so that a reload's new attempt takes a
	// change made meanwhile.
	infoDoc = mustDoc("p2.view-room.info", obj(schema.Properties{
		schema.Prop("from", schema.Int()), schema.Prop("bytes", schema.Int()), schema.Prop("keyed", schema.Int()),
		schema.Prop("textSeq", schema.Int()), schema.Prop("savedSeq", schema.Int()), schema.Prop("seen", schema.Int()),
	}), false, func() *delta.JsonObject {
		return delta.JsonObjectOf("from", 0.0, "bytes", 0.0, "keyed", 0.0, "textSeq", 0.0, "savedSeq", 0.0, "seen", -1.0)
	})
	// offersDoc is each peer's offered methods, numbered per peer (not a state member: only the agent reads them).
	offersDoc = mustDoc("p2.view-room.offers", obj(schema.Properties{
		schema.Prop("peers", p2runtime.Record(obj(schema.Properties{schema.Prop("n", schema.Int()), schema.Prop("methods", schema.Arr(contract.MethodInfoSchema))}))),
	}), false, func() *delta.JsonObject { return delta.JsonObjectOf("peers", delta.NewJsonObject(0)) })
)

// blockOf is a journal message's block key.
func blockOf(seq int) string { return itoa(seq / blockSize) }

func itoa(n int) string { return strconv.Itoa(n) }

// jsLen is a string's length in UTF-16 code units, as JavaScript's `.length`.
func jsLen(text string) int {
	units := 0
	for _, character := range text {
		if character >= 0x10000 {
			units += 2
		} else {
			units++
		}
	}
	return units
}

// intOf is a JSON number of object as an int (0 when absent).
func intOf(object *delta.JsonObject, key string) int {
	number, _ := object.Value(key).(float64)
	return int(number)
}

func draftInt(object *delta.Object, key string) int {
	number, _ := object.Get(key).(float64)
	return int(number)
}

// messageCost is a journal message's cost: its strings (UTF-16) and its entry.
func messageCost(message *delta.JsonObject) int {
	data, _ := message.Value("data").(string)
	from, _ := message.Value("from").(string)
	return 2*(jsLen(data)+jsLen(from)) + entryCost
}

// keptEntry is a keyed entry's strings in memory (UTF-16).
func keptEntry(key, data string) int { return 2 * (jsLen(key) + jsLen(data)) }

// jsonStringBytes is the UTF-8 bytes of text as a JSON string (`Buffer.byteLength(JSON.stringify(text))`): escapes included.
func jsonStringBytes(text string) int {
	bytes := 2
	for _, character := range text {
		switch {
		case character == '"' || character == '\\' || character == '\b' || character == '\f' || character == '\n' || character == '\r' || character == '\t':
			bytes += 2
		case character < 0x20:
			bytes += 6
		default:
			bytes += utf8.RuneLen(character)
		}
	}
	return bytes
}

// encodedEntry is a keyed entry's share of the keyed member's encoding as the wire carries it: the UTF-8 bytes of its key and
// data as JSON strings (escapes included) and the rest of the entry.
func encodedEntry(key, data string) int {
	return jsonStringBytes(key) + jsonStringBytes(data) + keyedEncoding
}

// encodedPeer is a peer's own share of the keyed member's encoding: its ID as a JSON key and its object's braces.
func encodedPeer(id string) int { return jsonStringBytes(id) + keyedPeer }

// jsonLen is the length of value's JSON text in UTF-16 code units (`JSON.stringify(value).length`).
func jsonLen(value delta.JsonValue) int {
	encoded, err := jsonText(value)
	if err != nil {
		return 0
	}
	return jsLen(string(encoded))
}

// roomFull: the budget refuses.
func roomFull() error {
	return data.NewPluginError("room_full", "The views' rooms keep too much memory now; try again once peers caught up (settings.json viewRoomsMemory)")
}

// notOpen: no frame that may answer shows the view.
func notOpen() error {
	return data.NewPluginError("not_open", "The view is not open in any browser that may answer")
}

// uncertain: "the call may or may not have run".
func uncertain() error {
	return data.NewPluginError("uncertain", "The view's leader left or changed its methods before answering: it may or may not have run")
}

// outcome is how a call ends: its frame's answer, or why not.
type outcome struct {
	result delta.JsonValue
	err    error
}

// serialQueue runs functions one at a time, in the order they asked (Pi's promise chain `writing`). A waiting caller parks with
// its handler (chordctx.Await), never blocks the host's section.
type serialQueue struct {
	mu   sync.Mutex
	tail chan chordctx.Settled[struct{}]
}

func (queue *serialQueue) run(ctx context.Context, fn func() error) error {
	queue.mu.Lock()
	previous := queue.tail
	done := make(chan chordctx.Settled[struct{}])
	queue.tail = done
	queue.mu.Unlock()
	if previous != nil {
		if _, err := chordctx.Await(ctx, previous); err != nil {
			// The caller gave up waiting: the line goes on once the one before it finished.
			go func() { <-previous; close(done) }()
			return err
		}
	}
	defer close(done)
	return fn()
}
