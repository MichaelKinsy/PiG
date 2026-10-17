// SPDX-License-Identifier: MIT

// Package boundarywire lets the subprocess handlers of one turn_end or agent_before_settle dispatch share the encoding of the context preview.
//
// Pi rebuilds the preview after every handler (runner.ts emitBoundary) and hands each handler the object. A subprocess handler receives it as JSON, so without sharing every handler encodes the whole projected history again. The Session records each preview it builds with the inputs it was projected from; a preview built from the same inputs as the one a subprocess handler already encoded has the same encoding, because each build is a fresh projection of the same session state.
//
// A preview built under a Cache carries each context entry's source as the entry's record (Built's sources) instead of a decoded value. Pi hands every handler the session's own entry objects (session-manager.ts buildSessionProjection), so a subprocess handler receives the record's members in the record's order. The dispatcher calls Deliver before a handler that reads Go values, which decodes the sources of that handler's preview in place; a dispatch whose handlers all read JSON decodes none.
package boundarywire

import (
	"context"
	"reflect"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Key names the inputs of the projected part of a preview: contextEntries, contextMessages and llmMessages. pendingMessages and canContinue are encoded for every handler.
type Key struct {
	// Session identifies the session log the preview was projected from.
	Session any
	// Revision is the log's revision, which every append and leaf move advances.
	Revision uint64
}

// identity tells one build's preview from another's: each build allocates its slices afresh.
type identity struct {
	entries  *extension.ProjectedSessionEntry
	messages *extension.AgentMessage
	llm      *ai.Message
}

func identityOf(preview extension.BoundaryContextPreview) (identity, bool) {
	if len(preview.ContextEntries) == 0 || len(preview.ContextMessages) == 0 {
		return identity{}, false
	}
	id := identity{entries: &preview.ContextEntries[0], messages: &preview.ContextMessages[0]}
	if len(preview.LLMMessages) > 0 {
		id.llm = &preview.LLMMessages[0]
	}
	return id, true
}

// Cache holds the encoding of the projected part of one dispatch's previews. Its methods are safe for concurrent use.
type Cache struct {
	mu sync.Mutex
	// latest is the preview built last and key the inputs it was built from; cacheable is false when the build had drafts or the session moved during it.
	latest    identity
	key       Key
	cacheable bool
	// wire encodes the projected part of a preview built from wireKey.
	wire    []byte
	wireKey Key
	hasWire bool
	// wireHandlerRan is set by a subprocess handler and cleared by each build. A build that finds it clear follows a handler that was not a subprocess handler: Go code that held a preview may have changed what later builds read, so the encoding is dropped.
	wireHandlerRan bool
	// entries is the latest preview's ContextEntries and sources their records; sources is nil when the preview was built with decoded sources. delivered is set once a handler that reads Go values received the latest preview: its decoded sources may since differ from the records.
	entries   []extension.ProjectedSessionEntry
	sources   []json.RawMessage
	delivered bool
	// projected is the latest preview's projected part. shared is set when its messages are the session's own objects.
	projected extension.BoundaryContextPreview
	shared    bool
	// encodings, when set, keeps the encodings of the session's messages from one dispatch to the next.
	encodings *Encodings
}

type cacheContextKey struct{}

// WithCache returns ctx carrying a new Cache for one boundary dispatch.
func WithCache(ctx context.Context) (context.Context, *Cache) {
	cache := &Cache{}
	return context.WithValue(ctx, cacheContextKey{}, cache), cache
}

// WithSessionCache is WithCache for a dispatch of the session whose encodings are kept in encodings.
func WithSessionCache(ctx context.Context, encodings *Encodings) (context.Context, *Cache) {
	ctx, cache := WithCache(ctx)
	cache.encodings = encodings
	return ctx, cache
}

// Encodings returns the session's kept encodings, or nil.
func (c *Cache) Encodings() *Encodings {
	return c.encodings
}

// FromContext returns the dispatch's Cache, or nil.
func FromContext(ctx context.Context) *Cache {
	if ctx == nil {
		return nil
	}
	cache, _ := ctx.Value(cacheContextKey{}).(*Cache)
	return cache
}

// Built records a preview the Session built. cacheable reports that key names every input of its projected part. sources, when not nil, holds the record of each of the preview's context entries, whose SourceEntry is left unset until Deliver decodes it.
func (c *Cache) Built(preview extension.BoundaryContextPreview, key Key, cacheable bool, sources []json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.wireHandlerRan {
		c.hasWire, c.wire = false, nil
	}
	c.wireHandlerRan = false
	id, ok := identityOf(preview)
	c.latest, c.key, c.cacheable = id, key, cacheable && ok
	c.entries, c.sources, c.delivered = preview.ContextEntries, sources, false
	c.projected = extension.BoundaryContextPreview{ContextEntries: preview.ContextEntries, ContextMessages: preview.ContextMessages, LLMMessages: preview.LLMMessages}
	c.shared = false
	if len(sources) != len(preview.ContextEntries) {
		c.sources = nil
	}
}

// Shared records that the latest build's messages are the session's own objects, which no handler may change: Deliver hands a handler that reads Go values copies.
func (c *Cache) Shared() {
	c.mu.Lock()
	c.shared = true
	c.mu.Unlock()
}

// Projected returns the projected part of the latest preview, its sources and whether its messages are the session's own when the next build, from the inputs key names, may reuse them instead of projecting the session again: the latest build was cacheable from the same inputs and held its sources as records, and no handler that reads Go values has received it, so no Go code held its values. Previews reach only the dispatch's handlers.
func (c *Cache) Projected(key Key) (preview extension.BoundaryContextPreview, sources []json.RawMessage, shared, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.cacheable || c.delivered || c.sources == nil || c.key != key {
		return extension.BoundaryContextPreview{}, nil, false, false
	}
	return c.projected, c.sources, c.shared, true
}

// Deliver prepares preview for a handler that reads its Go values: when preview is the latest build and its sources are still records, it decodes each into its entry's SourceEntry, in the preview's own entries; when its messages are the session's own objects, it puts copies in their place. Afterwards the preview's records no longer stand for its sources.
func (c *Cache) Deliver(preview extension.BoundaryContextPreview) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isLatest(preview) {
		return nil
	}
	if !c.delivered && c.sources != nil {
		for i, source := range c.sources {
			var value any
			if err := json.Unmarshal(source, &value); err != nil {
				return err
			}
			c.entries[i].SourceEntry = value
		}
	}
	if !c.delivered && c.shared {
		copyMessages(c.projected)
		c.shared = false
	}
	c.delivered = true
	return nil
}

// copyMessages puts copies of preview's messages in place of the messages in its slices. An entry's messages are the context messages in order (session-manager.ts buildSessionProjection), so both hold the same copies, and llmMessages are converted again from them.
func copyMessages(preview extension.BoundaryContextPreview) {
	copies := make([]extension.AgentMessage, len(preview.ContextMessages))
	for i, message := range preview.ContextMessages {
		copies[i] = message.Clone()
	}
	next := 0
	for i := range preview.ContextEntries {
		messages := preview.ContextEntries[i].Messages
		for j, message := range messages {
			if next < len(copies) && sameMessage(message, preview.ContextMessages[next]) {
				messages[j] = copies[next]
			} else {
				messages[j] = message.Clone()
			}
			next++
		}
	}
	copy(preview.ContextMessages, copies)
	if llm := agent.ConvertToLLM(copies); len(llm) == len(preview.LLMMessages) {
		copy(preview.LLMMessages, llm)
	}
}

// sameMessage reports whether a and b hold the same objects.
func sameMessage(a, b extension.AgentMessage) bool {
	if a.Custom != nil || b.Custom != nil {
		return a.Custom != nil && b.Custom != nil && reflect.ValueOf(a.Custom).UnsafePointer() == reflect.ValueOf(b.Custom).UnsafePointer()
	}
	return a.System == b.System && a.User == b.User && a.Assistant == b.Assistant && a.ToolResult == b.ToolResult
}

// Sources returns the records of preview's context entries when preview is the latest build, was built with them, and no handler that reads Go values has received it.
func (c *Cache) Sources(preview extension.BoundaryContextPreview) ([]json.RawMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isLatest(preview) || c.delivered || c.sources == nil {
		return nil, false
	}
	return c.sources, true
}

// Kept returns the session's kept encodings and preview's records when the encodings of preview's messages and records may be kept: preview is the latest build, was built with its records, and its messages are the session's own objects, which no handler that reads Go values has received.
func (c *Cache) Kept(preview extension.BoundaryContextPreview) (*Encodings, []json.RawMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.encodings == nil || !c.isLatest(preview) || c.delivered || !c.shared || c.sources == nil {
		return nil, nil, false
	}
	return c.encodings, c.sources, true
}

// isLatest reports whether preview's context entries are the latest build's.
func (c *Cache) isLatest(preview extension.BoundaryContextPreview) bool {
	return len(preview.ContextEntries) == len(c.entries) && (len(c.entries) == 0 || &preview.ContextEntries[0] == &c.entries[0])
}

// HandlerStarted records that a subprocess handler, which reads the preview only as JSON, is the one running after the latest build.
func (c *Cache) HandlerStarted() {
	c.mu.Lock()
	c.wireHandlerRan = true
	c.mu.Unlock()
}

// Encoded returns the encoding of preview's projected part when preview is the latest build, no handler that reads Go values has received it, and an earlier build from the same inputs was encoded.
func (c *Cache) Encoded(preview extension.BoundaryContextPreview) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := identityOf(preview); !ok || !c.cacheable || c.delivered || id != c.latest || !c.hasWire || c.wireKey != c.key {
		return nil, false
	}
	return c.wire, true
}

// Store keeps the encoding of preview's projected part when preview is the latest, cacheable build and no handler that reads Go values has received it.
func (c *Cache) Store(preview extension.BoundaryContextPreview, wire []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := identityOf(preview); !ok || !c.cacheable || c.delivered || id != c.latest {
		return
	}
	c.wire, c.wireKey, c.hasWire = wire, c.key, true
}
