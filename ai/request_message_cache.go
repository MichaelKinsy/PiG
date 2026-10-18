package ai

import (
	"bytes"
	"container/list"
	"encoding/binary"
	"encoding/json"
	"hash/maphash"
	"math"
	"slices"
	"sync"
)

// A request re-sends the whole conversation, and a message that is already in it does not change: a session of N turns encoded N(N+1)/2 messages. The encoder takes each small finalized message's encoding from this cache and writes a token where the message goes; the body is spliced from the cached bytes (stringSplice.join) and is the bytes marshalRequestBody gives the request.
//
// A message is looked up by what is encoded, not by where it came from: its key is a canonical record of every value of the converted message that its encoding reads (messageKey), so a message the conversion changed for any reason (the model, the API's compat flags, a cache_control position, thinking or redaction rules, a transform, a payload hook) has another key, and a cached encoding is served only to a message with the same key bytes. An entry keeps its key and a hit compares it byte for byte, so a hash collision costs an encoding and never serves another message's bytes.
// pig additive (D112): Pi serializes the whole request on every send; caching an encoding is a Go CPU choice and the body is unchanged.
const (
	// requestMessageMaxKeyBytes bounds the message that is cached: one that holds a large string is left to the string cache, which keys the string and not the message, so a tool result of megabytes is held once.
	requestMessageMaxKeyBytes = requestSpliceMinBytes
	// requestMessageCacheBytes bounds the bytes the message cache retains: each entry's key and its encoding.
	requestMessageCacheBytes = 32 << 20
	// requestMessageKeepRequests is how many request builds an entry outlives unused. Messages the last builds did not send belong to a history that a compaction, /new, /resume or branch switch left behind; the bound is larger than the string cache's because a message is cheap to keep and many subagents may alternate.
	requestMessageKeepRequests = 8
)

type encodedMessage struct {
	hash    uint64
	key     []byte
	encoded []byte
	// gen is the request build that last used the entry.
	gen uint64
}

func (entry *encodedMessage) size() int { return len(entry.key) + len(entry.encoded) }

// encodedMessageCache remembers the JSON encoding of finalized request messages by their key.
type encodedMessageCache struct {
	seed    maphash.Seed
	limit   int
	mu      sync.Mutex
	entries map[uint64]*list.Element
	recency list.List // front is the most recently used *encodedMessage
	held    int
	gen     uint64
	// misses counts the messages that were encoded, hits those found.
	misses, hits int
}

func newEncodedMessageCache(limit int) *encodedMessageCache {
	return &encodedMessageCache{seed: maphash.MakeSeed(), limit: limit, entries: map[uint64]*list.Element{}}
}

// beginRequest starts a request build: it forgets the entries that the last requestMessageKeepRequests builds did not use. They are at the back of recency, so it removes only them.
func (c *encodedMessageCache) beginRequest() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	for back := c.recency.Back(); back != nil && back.Value.(*encodedMessage).gen+requestMessageKeepRequests < c.gen; back = c.recency.Back() {
		c.forget(back)
	}
}

// lookup returns the encoding of the message with this key, or nil.
func (c *encodedMessageCache) lookup(hash uint64, key []byte) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[hash]
	if !ok {
		return nil
	}
	entry := element.Value.(*encodedMessage)
	if !bytes.Equal(entry.key, key) {
		return nil
	}
	c.recency.MoveToFront(element)
	entry.gen = c.gen
	c.hits++
	return entry.encoded
}

// store keeps the encoding of the message with this key. It keeps its own copy of key, which the caller reuses.
func (c *encodedMessageCache) store(hash uint64, key, encoded []byte) {
	entry := &encodedMessage{hash: hash, key: bytes.Clone(key), encoded: encoded}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.misses++
	if previous, ok := c.entries[hash]; ok {
		c.forget(previous)
	}
	entry.gen = c.gen
	c.entries[hash] = c.recency.PushFront(entry)
	c.held += entry.size()
	for c.held > c.limit && c.recency.Len() > 1 {
		c.forget(c.recency.Back())
	}
}

func (c *encodedMessageCache) forget(element *list.Element) {
	entry := c.recency.Remove(element).(*encodedMessage)
	delete(c.entries, entry.hash)
	c.held -= entry.size()
}

// release forgets every entry.
func (c *encodedMessageCache) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[uint64]*list.Element{}
	c.recency.Init()
	c.held = 0
}

// messageKey is the canonical record of a converted message: every value its encoding reads, each with a tag and, for text, its length, so two messages that differ in any value, or in where a value ends, have different records. A value the record does not know how to write (a type the converter does not produce, such as a payload hook's own) makes the message uncacheable, never keyed by less than it holds. Strings are copied, not referred to: a map or slice the caller owns may change after the record is made.
type messageKey struct {
	buf []byte
	// over is set when the record outgrew requestMessageMaxKeyBytes, and nothing more is written.
	over bool
	// keys is the stack of map member names being sorted, so a map of members is written in one order whatever its iteration order.
	keys []string
}

// reset empties the record for the next message.
func (k *messageKey) reset() {
	k.buf = k.buf[:0]
	k.over = false
	k.keys = k.keys[:0]
}

// ok reports whether the record is whole: it did not outgrow its bound.
func (k *messageKey) ok() bool { return !k.over }

const (
	keyNil byte = iota + 1
	keyString
	keyBool
	keyFloat
	keyInt
	keyRaw
	keyObject
	keyArray
	keyAbsent
	keyPresent
	keyBlocks
	keyParts
)

// tag, uvarint, text and raw are not inlined: the record writers call them at some hundred sites, and inlined each call site carried its own bounds check and append growth, which made the writers about 45 KB of the stripped binary (the size gate, test/perfgate) for a record whose cost is dwarfed by an encode.
//
//go:noinline
func (k *messageKey) tag(tag byte) {
	if !k.over {
		k.buf = append(k.buf, tag)
	}
}

//go:noinline
func (k *messageKey) uvarint(n uint64) {
	if !k.over {
		k.buf = binary.AppendUvarint(k.buf, n)
	}
}

// text writes s, which is any string of the message.
//
//go:noinline
func (k *messageKey) text(s string) {
	if k.over {
		return
	}
	if len(k.buf)+len(s) > requestMessageMaxKeyBytes {
		k.over = true
		return
	}
	k.buf = binary.AppendUvarint(k.buf, uint64(len(s)))
	k.buf = append(k.buf, s...)
}

//go:noinline
func (k *messageKey) raw(raw []byte) {
	if k.over {
		return
	}
	if len(k.buf)+len(raw) > requestMessageMaxKeyBytes {
		k.over = true
		return
	}
	k.buf = binary.AppendUvarint(k.buf, uint64(len(raw)))
	k.buf = append(k.buf, raw...)
}

func (k *messageKey) boolean(b bool) {
	if b {
		k.tag(1)
	} else {
		k.tag(0)
	}
}

// present writes whether a pointer or a slice is nil, which the encoding tells apart.
func (k *messageKey) present(is bool) {
	if is {
		k.tag(keyPresent)
	} else {
		k.tag(keyAbsent)
	}
}

func (k *messageKey) optionalBool(b *bool) {
	k.present(b != nil)
	if b != nil {
		k.boolean(*b)
	}
}

func (k *messageKey) optionalText(s *string) {
	k.present(s != nil)
	if s != nil {
		k.text(*s)
	}
}

// length writes the length of a slice, and whether it is nil.
func (k *messageKey) length(n int, isNil bool) {
	k.present(!isNil)
	k.uvarint(uint64(n))
}

// order writes the recorded member order of a schema, whose objects the encoding writes in it.
func (k *messageKey) order(order schemaObjectOrder) {
	k.present(order != nil)
	if order == nil {
		return
	}
	paths := slices.Sorted(func(yield func(string) bool) {
		for path := range order {
			if !yield(path) {
				return
			}
		}
	})
	k.uvarint(uint64(len(paths)))
	for _, path := range paths {
		k.text(path)
		k.length(len(order[path]), order[path] == nil)
		for _, member := range order[path] {
			k.text(member)
		}
	}
}

// object writes the members of a decoded JSON object in the order of their names.
func (k *messageKey) object(members map[string]any) bool {
	k.present(members != nil)
	if members == nil {
		return true
	}
	k.uvarint(uint64(len(members)))
	at := len(k.keys)
	for name := range members {
		k.keys = append(k.keys, name)
	}
	names := k.keys[at:]
	slices.Sort(names)
	for _, name := range names {
		if k.over {
			break
		}
		k.text(name)
		// A nested object sorts its own names above this one's, which stay in place below it.
		if !k.json(members[name]) {
			k.keys = k.keys[:at]
			return false
		}
	}
	k.keys = k.keys[:at]
	return true
}

// json writes a value of the decoded JSON kinds. It reports false for any other, which the message's encoding may read in ways the record does not.
func (k *messageKey) json(value any) bool {
	switch value := value.(type) {
	case nil:
		k.tag(keyNil)
	case string:
		k.tag(keyString)
		k.text(value)
	case bool:
		k.tag(keyBool)
		k.boolean(value)
	case float64:
		k.tag(keyFloat)
		k.uvarint(math.Float64bits(value))
	case int:
		k.tag(keyInt)
		k.uvarint(uint64(value))
	case json.RawMessage:
		k.tag(keyRaw)
		k.present(value != nil)
		k.raw(value)
	case map[string]any:
		k.tag(keyObject)
		return k.object(value)
	case []any:
		k.tag(keyArray)
		k.length(len(value), value == nil)
		for _, element := range value {
			if k.over {
				break
			}
			if !k.json(element) {
				return false
			}
		}
	default:
		return false
	}
	return true
}
