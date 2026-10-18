package ai

import (
	"bytes"
	"container/list"
	"crypto/rand"
	"encoding/hex"
	"hash/maphash"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"
)

// A request re-sends the whole conversation, so encoding every earlier tool result's text again on every turn made a long session's per-turn CPU grow with its history. The completions request encodes each large message string once and splices the cached encoding into the body. The bytes are identical to what marshalRequestBody (JSON.stringify) of the same request produces.
const (
	// requestSpliceMinBytes is the smallest string worth a cache entry.
	// pig additive (D112): Pi serializes the whole request on every send; caching an encoding is a Go CPU choice and the body is unchanged.
	requestSpliceMinBytes = 4 << 10
	// requestSpliceCacheBytes bounds the bytes the cache retains: each entry's source string and its encoding.
	// pig additive (D112): see requestSpliceMinBytes.
	requestSpliceCacheBytes = 64 << 20
	// requestSpliceKeepRequests is how many request builds an entry outlives unused. A build re-sends the conversation, so an entry the last
	// builds did not use belongs to history that a compaction, /new, /resume or branch switch left behind. Two keep a second session or a
	// subagent that alternates with the first warm.
	// pig additive (D112): see requestSpliceMinBytes.
	requestSpliceKeepRequests = 2
)

type quotedStringKey struct {
	size int
	hash uint64
}

type quotedString struct {
	key    quotedStringKey
	source string
	quoted []byte
	// gen is the request build that last used the entry.
	gen uint64
}

// quotedStringCache remembers the JSON encoding of large strings. The entry keeps its source string and a hit requires equal content, so a hash collision costs an encoding and never serves another string's bytes. Eviction is least recently used and constant time, so a working set larger than the limit costs an encoding per string, as the uncached path does, and never a scan of the cache. An entry that requestSpliceKeepRequests request builds in a row did not use is evicted too, so history a session no longer sends does not stay until the limit pushes it out.
type quotedStringCache struct {
	seed    maphash.Seed
	limit   int
	mu      sync.Mutex
	entries map[quotedStringKey]*list.Element
	// bySource indexes the same entries by the address and length of their source string. A Go string never changes and the entry keeps its source alive, so a string at that address with that length is the source: a request that re-sends the message strings of the previous one finds their encodings without hashing or comparing them.
	bySource map[stringIdentity]*list.Element
	// byData indexes the same entries by the first byte of their encoding, so a request that carries an encoding as raw JSON can be recognized.
	byData map[*byte]*list.Element
	// messages holds the encodings of the request's small finalized messages (encodedMessageCache), which a request that has them does not encode again; nil keeps none, and a message is encoded as it is without the cache.
	messages *encodedMessageCache
	recency  list.List // front is the most recently used *quotedString
	held     int
	misses   int
	// tokens is the number of large strings the last request build spliced, which sizes the next build's list of them.
	tokens atomic.Int64
	// gen counts request builds (beginRequest). A use stamps the entry with it, and recency moves the entry to the front, so the stamps
	// never increase from the front of recency to its back.
	gen uint64
}

// stringIdentity is a string's data address and length.
type stringIdentity struct {
	data *byte
	size int
}

func identityOf(s string) stringIdentity { return stringIdentity{unsafe.StringData(s), len(s)} } //nolint:gosec // G103: the address and length identify a string in a cache key; the pointer is never dereferenced

func newQuotedStringCache(limit int) *quotedStringCache {
	return &quotedStringCache{seed: maphash.MakeSeed(), limit: limit, entries: map[quotedStringKey]*list.Element{}, bySource: map[stringIdentity]*list.Element{}, byData: map[*byte]*list.Element{}, messages: newEncodedMessageCache(requestMessageCacheBytes)}
}

// requestStrings is the process-wide cache: each request builds a new provider, so a per-provider cache would never be reused.
var requestStrings = newQuotedStringCache(requestSpliceCacheBytes)

func (entry *quotedString) size() int { return len(entry.source) + len(entry.quoted) }

// quote returns the JSON.stringify encoding of s, as marshalRequestBody writes it.
func (c *quotedStringCache) quote(s string) []byte {
	if quoted := c.sourced(s); quoted != nil {
		return quoted
	}
	key := quotedStringKey{len(s), maphash.String(c.seed, s)}
	if quoted := c.lookup(key, s); quoted != nil {
		return quoted
	}
	return c.encode(key, s)
}

// quoteValid is quote for a string that may not be UTF-8: it reports false, and caches nothing, for one that is not. A string the cache holds was checked when it was encoded, so a hit checks nothing.
func (c *quotedStringCache) quoteValid(s string) ([]byte, bool) {
	if quoted := c.sourced(s); quoted != nil {
		return quoted, true
	}
	key := quotedStringKey{len(s), maphash.String(c.seed, s)}
	if quoted := c.lookup(key, s); quoted != nil {
		return quoted, true
	}
	if !utf8.ValidString(s) {
		return nil, false
	}
	return c.encode(key, s), true
}

// sourced returns the encoding of the entry whose source is s itself, found by its identity, or nil.
func (c *quotedStringCache) sourced(s string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.bySource[identityOf(s)]
	if !ok {
		return nil
	}
	entry := element.Value.(*quotedString)
	if identityOf(entry.source) != identityOf(s) {
		return nil
	}
	c.recency.MoveToFront(element)
	entry.gen = c.gen
	return entry.quoted
}

// known returns the encoding of s when the cache holds this very string.
func (c *quotedStringCache) known(s string) ([]byte, bool) {
	quoted := c.sourced(s)
	return quoted, quoted != nil
}

// lookup returns the encoding of the entry with key whose source equals s, or nil. A hit by an equal copy makes the copy the entry's source, so the next request that re-sends the copy finds it by identity.
func (c *quotedStringCache) lookup(key quotedStringKey, s string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil
	}
	entry := element.Value.(*quotedString)
	if entry.source != s {
		return nil
	}
	c.recency.MoveToFront(element)
	entry.gen = c.gen
	if identityOf(entry.source) != identityOf(s) {
		c.unindexSource(element)
		entry.source = s
		c.bySource[identityOf(s)] = element
	}
	return entry.quoted
}

// encode encodes s, which no entry holds, and keeps the encoding.
func (c *quotedStringCache) encode(key quotedStringKey, s string) []byte {
	quoted, err := marshalRequestBody(s)
	if err != nil {
		panic("ai: encoding a string failed: " + err.Error())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.misses++
	if previous, ok := c.entries[key]; ok {
		c.forget(previous)
	}
	entry := &quotedString{key: key, source: s, quoted: quoted, gen: c.gen}
	element := c.recency.PushFront(entry)
	c.entries[key] = element
	c.bySource[identityOf(s)] = element
	c.byData[&quoted[0]] = element
	c.held += entry.size()
	for c.held > c.limit && c.recency.Len() > 1 {
		c.forget(c.recency.Back())
	}
	return quoted
}

// beginRequest starts a request build: it forgets the entries that the last requestSpliceKeepRequests builds did not use. They are at the
// back of recency, so it removes only them.
func (c *quotedStringCache) beginRequest() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	for back := c.recency.Back(); back != nil && back.Value.(*quotedString).gen+requestSpliceKeepRequests < c.gen; back = c.recency.Back() {
		c.forget(back)
	}
}

// ReleaseRequestCaches forgets every encoding the request caches hold: large strings and finalized messages. The next request encodes what it sends again, as it would have the first time; a build that runs meanwhile finds nothing to splice and encodes its values itself, so its body is the same.
// pig additive (D112): an interactive session that went idle calls it from its idle collection (D172), so the encodings of the conversation it sent do not stay live while it waits.
func ReleaseRequestCaches() {
	requestStrings.release()
}

func (c *quotedStringCache) release() {
	c.mu.Lock()
	c.entries = map[quotedStringKey]*list.Element{}
	c.bySource = map[stringIdentity]*list.Element{}
	c.byData = map[*byte]*list.Element{}
	c.recency.Init()
	c.held = 0
	c.mu.Unlock()
	if c.messages != nil {
		c.messages.release()
	}
}

func (c *quotedStringCache) forget(element *list.Element) {
	entry := c.recency.Remove(element).(*quotedString)
	delete(c.entries, entry.key)
	c.unindexSource(element)
	delete(c.byData, &entry.quoted[0])
	c.held -= entry.size()
}

// unindexSource removes the identity index of element's source, unless the index names another entry.
func (c *quotedStringCache) unindexSource(element *list.Element) {
	identity := identityOf(element.Value.(*quotedString).source)
	if c.bySource[identity] == element {
		delete(c.bySource, identity)
	}
}

// holds reports whether raw is, byte for byte and by identity, an encoding this cache handed out and still keeps.
func (c *quotedStringCache) holds(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.byData[&raw[0]]
	return ok && len(element.Value.(*quotedString).quoted) == len(raw)
}

// requestBody is a request body held as the segments it is made of, so that sending it does not first copy a long conversation into one buffer. The segments are never modified.
type requestBody struct {
	segments [][]byte
	size     int
}

func (b *requestBody) add(segment []byte) {
	b.segments = append(b.segments, segment)
	b.size += len(segment)
}

// Bytes returns the whole body in one buffer.
func (b *requestBody) Bytes() []byte {
	if len(b.segments) == 1 {
		return b.segments[0]
	}
	return bytes.Join(b.segments, nil)
}

// reader returns a reader of the body from its start. A body of one segment is a *bytes.Reader, which net/http sends in the same write as the request headers; only a body of several segments needs its own reader.
func (b *requestBody) reader() io.Reader {
	if len(b.segments) == 1 {
		return bytes.NewReader(b.segments[0])
	}
	return &requestBodyReader{segments: b.segments}
}

// prepare gives a request built from reader the length of the body and a way to resend it after a redirect, which net/http derives itself only for a *bytes.Reader.
func (b *requestBody) prepare(request *http.Request) {
	if len(b.segments) == 1 {
		return
	}
	request.ContentLength = int64(b.size)
	request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(b.reader()), nil }
}

// requestBodyReader reads a requestBody. WriteTo lets io.Copy hand each segment to the connection without an intermediate copy.
type requestBodyReader struct {
	segments [][]byte
	index    int
	offset   int
}

func (r *requestBodyReader) Read(p []byte) (int, error) {
	n := 0
	for r.index < len(r.segments) && n < len(p) {
		copied := copy(p[n:], r.segments[r.index][r.offset:])
		n += copied
		r.offset += copied
		if r.offset == len(r.segments[r.index]) {
			r.index++
			r.offset = 0
		}
	}
	if n == 0 && len(p) > 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (r *requestBodyReader) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for r.index < len(r.segments) {
		n, err := w.Write(r.segments[r.index][r.offset:])
		total += int64(n)
		r.offset += n
		if err != nil {
			return total, err
		}
		r.index++
		r.offset = 0
	}
	return total, nil
}

// spliceToken is the JSON string that stands for the index-th large string; the closing quote keeps index 1 from matching index 10.
func spliceToken(nonce string, index int) string {
	return `"` + nonce + "-" + strconv.Itoa(index) + `"`
}

func newSpliceNonce() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("ai: random source failed: " + err.Error())
	}
	return "pig-splice-" + hex.EncodeToString(raw[:])
}
