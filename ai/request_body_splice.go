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

	"github.com/MichaelKinsy/PiG/internal/jsparity"
)

// A request re-sends the whole conversation, so encoding every earlier tool result's text again on every turn made a long session's per-turn CPU grow with its history. The completions request encodes each large message string once and splices the cached encoding into the body. The bytes are identical to what marshalRequestBody (JSON.stringify) of the same request produces.
const (
	// requestSpliceMinBytes is the smallest string worth a cache entry.
	// pig additive (D112): Pi serializes the whole request on every send; caching an encoding is a Go CPU choice and the body is unchanged.
	requestSpliceMinBytes = 4 << 10
	// requestSpliceCacheBytes bounds the bytes the cache retains: each entry's source string and its encoding.
	// pig additive (D112): see requestSpliceMinBytes.
	requestSpliceCacheBytes = 64 << 20
	// requestSpliceMaxCopies bounds the equal copies of one string whose address an entry remembers beyond its source. A conversation that reads one file again and again holds one equal copy per read; each is found by identity, so a request that sends them all hashes and compares none. Past the bound the oldest copies are forgotten, so a request that carries more equal copies than the bound finds them by content.
	// pig additive (D112): see requestSpliceMinBytes.
	requestSpliceMaxCopies = 256
	// requestSpliceKeepRequests is how many request builds an entry outlives unused. A build re-sends the conversation, so an entry the last
	// builds did not use belongs to history that a compaction, /new, /resume or branch switch left behind. Two keep a second session or a
	// subagent that alternates with the first warm.
	// pig additive (D112): see requestSpliceMinBytes.
	requestSpliceKeepRequests = 2
	// requestSpliceUnsentBytes bounds the bytes of entries that a tool result's encoding made (QuotedPlainText, a session entry) and no request build has used yet. The request that follows a read uses them at once, so the bound is only a process that reads and never sends: it keeps this much and not requestSpliceCacheBytes.
	// pig additive (D112): see requestSpliceMinBytes.
	requestSpliceUnsentBytes = 4 << 20
)

type quotedStringKey struct {
	size int
	hash uint64
}

type quotedString struct {
	key    quotedStringKey
	source string
	// copies are strings equal to source that earlier requests carried at other addresses, oldest first. The entry keeps them alive, like source, so their addresses stay theirs.
	copies []string
	// copyBytes is the length of every string in copies.
	copyBytes int
	quoted    []byte
	// plain is set when quoted is also what encoding/json writes for source with HTML escaping off: the two forms differ only for U+2028 and
	// U+2029, which JSON.stringify writes as they are, and for text that is not UTF-8, which the cache never holds. A tool's structured content
	// is written in the encoding/json form, so it can be this very slice when plain is set.
	plain bool
	// gen is the request build that last used the entry.
	gen uint64
	// pending is the entry's place in the cache's unsent list, nil once a request build used it; pendingBytes is what it counts there.
	pending      *list.Element
	pendingBytes int
}

// quotedStringCache remembers the JSON encoding of large strings. The entry keeps its source string and a hit requires equal content, so a hash collision costs an encoding and never serves another string's bytes. Eviction is least recently used and constant time, so a working set larger than the limit costs an encoding per string, as the uncached path does, and never a scan of the cache. An entry that requestSpliceKeepRequests request builds in a row did not use is evicted too, so history a session no longer sends does not stay until the limit pushes it out.
type quotedStringCache struct {
	seed    maphash.Seed
	limit   int
	mu      sync.Mutex
	entries map[quotedStringKey]*list.Element
	// bySource indexes the same entries by the address and length of their source string and of its copies. A Go string never changes and the entry keeps each of them alive, so a string at that address with that length is that string: a request that re-sends the message strings of the previous one finds their encodings without hashing or comparing them.
	bySource map[stringIdentity]*list.Element
	// byData indexes the same entries by the first byte of their encoding, so a request that carries an encoding as raw JSON can be recognized.
	byData map[*byte]*list.Element
	// messages holds the encodings of the request's small finalized messages (encodedMessageCache), which a request that has them does not encode again; nil keeps none, and a message is encoded as it is without the cache.
	messages *encodedMessageCache
	recency  list.List // front is the most recently used *quotedString
	held     int
	// pending lists, newest first, the recency elements of the unsent entries: those a caller that is not a request build made and no request build has used. heldUnsent is their bytes; trimUnsent holds it to requestSpliceUnsentBytes.
	pending    list.List
	heldUnsent int
	misses     int
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

func (entry *quotedString) size() int { return len(entry.source) + entry.copyBytes + len(entry.quoted) }

// quote returns the JSON.stringify encoding of s, as marshalRequestBody writes it.
func (c *quotedStringCache) quote(s string) []byte {
	if entry := c.sourcedEntry(s, false); entry != nil {
		return entry.quoted
	}
	key := quotedStringKey{len(s), maphash.String(c.seed, s)}
	if entry := c.lookupEntry(key, s, false); entry != nil {
		return entry.quoted
	}
	return c.encode(key, s, utf8.ValidString(s), false).quoted
}

// quoteValid is quote for a string that may not be UTF-8: it reports false, and caches nothing, for one that is not. A string the cache holds was checked when it was encoded, so a hit checks nothing.
func (c *quotedStringCache) quoteValid(s string) ([]byte, bool) {
	if entry := c.quoteValidEntry(s, false); entry != nil {
		return entry.quoted, true
	}
	return nil, false
}

// quoteValidEarly is quoteValid for a caller that is not a request build (the session entry of a tool result): see quoteValidEntry.
func (c *quotedStringCache) quoteValidEarly(s string) ([]byte, bool) {
	if entry := c.quoteValidEntry(s, true); entry != nil {
		return entry.quoted, true
	}
	return nil, false
}

// quoteValidEntry is quoteValid's entry, nil for a string that is not UTF-8. An entry's quoted and plain never change, so the caller reads them without the lock. early marks a caller that is not a request build: an entry it makes is unsent until a request build uses it, and the unsent entries are held to requestSpliceUnsentBytes, oldest forgotten first, so a process that reads files and sends no request keeps a few MiB and not the cache's whole limit. A hit by a request build makes the entry sent.
func (c *quotedStringCache) quoteValidEntry(s string, early bool) *quotedString {
	if entry := c.sourcedEntry(s, early); entry != nil {
		return entry
	}
	key := quotedStringKey{len(s), maphash.String(c.seed, s)}
	if entry := c.lookupEntry(key, s, early); entry != nil {
		return entry
	}
	if !utf8.ValidString(s) {
		return nil
	}
	return c.encode(key, s, true, early)
}

// quotePlain is the encoding of s that JSON.stringify and encoding/json (HTML escaping off) both write, for a string of at least requestSpliceMinBytes
// that is UTF-8 and holds neither U+2028 nor U+2029; it reports false, and caches nothing it does not need, for any other. The slice is the
// cache's own and is not to be written to.
func (c *quotedStringCache) quotePlain(s string) ([]byte, bool) {
	if len(s) < requestSpliceMinBytes {
		return nil, false
	}
	if entry := c.quoteValidEntry(s, true); entry != nil && entry.plain {
		return entry.quoted[:len(entry.quoted):len(entry.quoted)], true
	}
	return nil, false
}

// sourced returns the encoding of the entry whose source is s itself, found by its identity, or nil.
func (c *quotedStringCache) sourced(s string) []byte {
	if entry := c.sourcedEntry(s, false); entry != nil {
		return entry.quoted
	}
	return nil
}

// sourcedEntry is the entry whose source is s itself, found by its identity, or nil.
func (c *quotedStringCache) sourcedEntry(s string, early bool) *quotedString {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.bySource[identityOf(s)]
	if !ok {
		return nil
	}
	entry := element.Value.(*quotedString)
	c.recency.MoveToFront(element)
	entry.gen = c.gen
	if !early {
		c.markSent(entry)
	}
	return entry
}

// known returns the encoding of s when the cache holds this very string.
func (c *quotedStringCache) known(s string) ([]byte, bool) {
	quoted := c.sourced(s)
	return quoted, quoted != nil
}

// lookupEntry returns the entry with key whose source equals s, or nil. A hit by an equal copy at another address makes the entry remember that address too, so the next request that re-sends the copy finds it by identity, and so does each other copy the conversation holds.
func (c *quotedStringCache) lookupEntry(key quotedStringKey, s string, early bool) *quotedString {
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
	if early {
		return entry
	}
	c.markSent(entry)
	if _, indexed := c.bySource[identityOf(s)]; !indexed {
		c.addCopy(element, entry, s)
	}
	return entry
}

// addCopy makes the address of s, which equals entry's source, an address the entry finds by identity. The entry remembers requestSpliceMaxCopies of them: the oldest is forgotten for the next. c.mu must be held.
func (c *quotedStringCache) addCopy(element *list.Element, entry *quotedString, s string) {
	if len(entry.copies) >= requestSpliceMaxCopies {
		oldest := entry.copies[0]
		if c.bySource[identityOf(oldest)] == element {
			delete(c.bySource, identityOf(oldest))
		}
		entry.copyBytes -= len(oldest)
		c.held -= len(oldest)
		last := len(entry.copies) - 1
		copy(entry.copies, entry.copies[1:])
		entry.copies[last] = ""
		entry.copies = entry.copies[:last]
	}
	entry.copies = append(entry.copies, s)
	entry.copyBytes += len(s)
	c.held += len(s)
	c.bySource[identityOf(s)] = element
	for c.held > c.limit && c.recency.Len() > 1 {
		c.forget(c.recency.Back())
	}
}

// encode encodes s, which no entry holds, and keeps the encoding; valid reports that s is UTF-8, early that the caller is not a request build (quoteValidEntry).
func (c *quotedStringCache) encode(key quotedStringKey, s string, valid, early bool) *quotedString {
	var quoted []byte
	plain := false
	if valid {
		quoted = quoteValidText(s)
		plain = !hasLineSeparator(quoted)
	} else {
		var err error
		if quoted, err = marshalRequestBody(s); err != nil {
			panic("ai: encoding a string failed: " + err.Error())
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.misses++
	if previous, ok := c.entries[key]; ok {
		c.forget(previous)
	}
	entry := &quotedString{key: key, source: s, quoted: quoted, plain: plain, gen: c.gen}
	element := c.recency.PushFront(entry)
	c.entries[key] = element
	c.bySource[identityOf(s)] = element
	c.byData[&quoted[0]] = element
	c.held += entry.size()
	if early {
		entry.pending, entry.pendingBytes = c.pending.PushFront(element), entry.size()
		c.heldUnsent += entry.pendingBytes
		c.trimUnsent()
	}
	for c.held > c.limit && c.recency.Len() > 1 {
		c.forget(c.recency.Back())
	}
	return entry
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
	c.pending.Init()
	c.held, c.heldUnsent = 0, 0
	c.mu.Unlock()
	if c.messages != nil {
		c.messages.release()
	}
}

func (c *quotedStringCache) forget(element *list.Element) {
	entry := c.recency.Remove(element).(*quotedString)
	delete(c.entries, entry.key)
	c.unindexSource(entry, element)
	delete(c.byData, &entry.quoted[0])
	c.held -= entry.size()
	c.markSent(entry)
}

// markSent takes entry out of the unsent entries: a request build used it, or it is forgotten. c.mu must be held.
func (c *quotedStringCache) markSent(entry *quotedString) {
	if entry.pending == nil {
		return
	}
	c.pending.Remove(entry.pending)
	c.heldUnsent -= entry.pendingBytes
	entry.pending, entry.pendingBytes = nil, 0
}

// trimUnsent forgets the oldest unsent entries while they hold more than requestSpliceUnsentBytes, keeping the newest. c.mu must be held.
func (c *quotedStringCache) trimUnsent() {
	for c.heldUnsent > requestSpliceUnsentBytes && c.pending.Len() > 1 {
		c.forget(c.pending.Back().Value.(*list.Element))
	}
}

// unindexSource removes the identity index of entry's source and copies, unless an index names another entry.
func (c *quotedStringCache) unindexSource(entry *quotedString, element *list.Element) {
	if identity := identityOf(entry.source); c.bySource[identity] == element {
		delete(c.bySource, identity)
	}
	for _, s := range entry.copies {
		if identity := identityOf(s); c.bySource[identity] == element {
			delete(c.bySource, identity)
		}
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
	request.GetBody = func() (io.ReadCloser, error) { return &requestBodyReader{segments: b.segments}, nil }
}

// requestBodyReader reads a requestBody. WriteTo lets io.Copy hand each segment to the connection without an intermediate copy.
type requestBodyReader struct {
	segments [][]byte
	index    int
	offset   int
}

// Close does nothing. It makes the reader an io.ReadCloser, so http.NewRequest uses it as the body and the transport hands it to idleTimeoutConn.ReadFrom unwrapped.
func (r *requestBodyReader) Close() error { return nil }

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

// quoteScratch holds the buffers quoteValidText encodes into before it copies the encoding out at its exact length: a cached encoding lives as
// long as the conversation holds its text, so it holds no spare room.
var quoteScratch sync.Pool

// maxPooledQuoteScratch bounds the buffer kept between encodings (golang/go#23199); a longer text encodes into a buffer that is dropped.
// pig additive (D112): see requestSpliceMinBytes.
const maxPooledQuoteScratch = 256 << 10

// quoteValidText is marshalRequestBody(s) for a string that is UTF-8: JSON.stringify's text, with every character but the quote, the backslash
// and the C0 controls as it is. The result is a new slice of exactly its length.
func quoteValidText(s string) []byte {
	buffer, _ := quoteScratch.Get().(*[]byte)
	if buffer == nil {
		buffer = new([]byte)
	}
	encoded := jsparity.AppendString((*buffer)[:0], s)
	quoted := bytes.Clone(encoded)
	if cap(encoded) <= maxPooledQuoteScratch {
		*buffer = encoded[:0]
		quoteScratch.Put(buffer)
	}
	return quoted
}

// hasLineSeparator reports whether the JSON text holds U+2028 or U+2029, which JSON.stringify writes as they are and encoding/json escapes.
func hasLineSeparator(quoted []byte) bool {
	for rest := quoted; ; {
		at := bytes.IndexByte(rest, 0xe2)
		if at < 0 || at+2 >= len(rest) {
			return false
		}
		if rest[at+1] == 0x80 && (rest[at+2] == 0xa8 || rest[at+2] == 0xa9) {
			return true
		}
		rest = rest[at+1:]
	}
}

// QuotedPlainText returns the JSON encoding, quotes included, of a text that the session's entries, the request and a tool's structured content
// all write: the bytes JSON.stringify writes and encoding/json writes with HTML escaping off, which differ only for U+2028 and U+2029 and for
// text that is not UTF-8. It is the encoding the request cache keeps, made at the first ask and found again by the text's identity, so a tool
// that hands its result to the agent asks for it once and the session entry and the request that follow find it made. It reports false for a
// text shorter than requestSpliceMinBytes and for one of the two kinds that differ. The slice is the cache's own: it must not be written to, and it
// has no spare room to append into.
// pig additive (D112): see requestSpliceMinBytes.
func QuotedPlainText(text string) ([]byte, bool) { return requestStrings.quotePlain(text) }
