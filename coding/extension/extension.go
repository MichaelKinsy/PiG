package extension

import (
	"maps"
	"sync"
)

// ExtensionFactory is an extension written as Go code the binary was built with: it receives the extension API and registers tools, commands,
// flags, shortcuts, providers and event handlers on it, and returns the error upstream's factory throws.
//
// Only the source of a binary's own main package supplies one, by value, to the loader in package factoryload
// (docs/specs/extension-factory-trust.md). No run-time input, Package, Piglet or wire peer can produce one. The subprocess SDKs keep upstream's
// `pi` argument on their own side of the wire.
//
// upstream: core/extensions/types.ts:2017 (ExtensionFactory)
type ExtensionFactory func(api API) error

// InlineExtension is types.ts:2008 `InlineExtension`: an [ExtensionFactory], or a [NamedInlineExtension] that also names the extension and says how it loads.
// The set is closed: only those two forms implement it.
type InlineExtension interface{ isInlineExtension() }

func (ExtensionFactory) isInlineExtension() {}

// NamedInlineExtension is the object member of types.ts:2008 `InlineExtension`.
type NamedInlineExtension struct {
	// Name is shown as `<inline:name>` in the startup Extensions list and errors. With Builtin, the extension is named `builtin:name` in errors and diagnostics.
	Name    string
	Factory ExtensionFactory
	// Hidden omits the extension from the startup Extensions list.
	Hidden bool
	// Replaceable leaves the extension out when another extension registers a tool, command or flag with a name it registers during loading, instead
	// of reporting a conflict. The factory still runs, so it should only register tools, commands, flags and event handlers.
	Replaceable bool
	// Builtin supplies the code of the `builtin:<name>` extension resource instead of loading as an inline extension: it loads by default, `pig config`
	// lists it, `-builtin:<name>` in the `extensions` setting and --no-extensions disable it, and `-e builtin:<name>` loads it explicitly. It is hidden
	// from the startup Extensions list and loads after project trust is resolved, so it cannot handle `project_trust`.
	Builtin bool
}

func (NamedInlineExtension) isInlineExtension() {}

// Extension is the state container populated by an extension's factory
// during loading. It is the cross-tier contract between the host and the
// extension implementation: factories write registrations into the API,
// the API writes them into this struct, and the Runner reads from it
// during dispatch.
//
// A pre-populated `[]Extension` is what gets handed to a Runner. Loaders
// produce these; runners consume them. The runner does not mutate them.
//
// Field-level translation rule: Go's idiomatic field naming and JSON tag
// preservation are applied; see docs/parity/DIVERGENCES.md "TS→Go translation
// rituals" section. The maps, sets, and overall shape mirror upstream
// verbatim.
//
// upstream: types.ts:1513-1523 (export interface Extension)
type Extension struct {
	// Name is the human-readable name of the extension, typically derived
	// from the manifest or directory name. Used for display in the UI.
	Name string

	// Path is the original (unresolved) path the extension was loaded from.
	// This is typically the path the user passed via --extension or that the
	// loader discovered. May be relative.
	Path string

	// ResolvedPath is the absolute, canonical path used for diagnostics,
	// telemetry, and "Using <path>" UI strings.
	ResolvedPath string

	// Replaceable leaves the extension out when another extension registers a
	// tool, command, or flag with a name it registers during loading, instead of
	// reporting a conflict. The CLI's built-in MCP, codemode, and tool search
	// extensions use it. upstream: types.ts:2209 (Extension.replaceable)
	Replaceable bool

	// SourceInfo describes the extension's origin and ownership metadata. The
	// current public contract carries this value opaquely.
	SourceInfo SourceInfo

	// Handlers is the legacy construction shape. NewRunner imports it into the
	// concurrency-safe handler registry before dispatch.
	Handlers map[string][]HandlerFn

	handlerState *eventHandlerState

	// Tools is the startup construction shape, keyed by name. Runtime readers use RegisteredTools or RegisteredTool to include synchronized late registrations.
	Tools map[string]RegisteredTool

	toolState *toolRegistry

	// ToolOrder records tool registration order. Go maps do not retain
	// insertion order; loaders populate this alongside Tools so tool lists
	// keep upstream's registration order (a Map in extension.tools).
	ToolOrder []string

	// MessageRenderers is keyed by the custom message type the renderer handles.
	MessageRenderers map[string]MessageRenderer

	// EntryRenderers is keyed by the custom entry type the renderer handles.
	// Custom entries (appended via AppendEntry) do not participate in LLM context.
	EntryRenderers map[string]EntryRenderer

	// ToolRenderers are the extension's tool renderer resolvers, in registration order.
	// upstream: types.ts Extension.toolRenderers
	ToolRenderers []ToolRendererResolver

	// MarkdownTransformer is the extension's display-only Markdown transform,
	// if it registered one; a later registration replaces an earlier one.
	// upstream: types.ts Extension.markdownTransformer
	MarkdownTransformer MarkdownTransformer

	// Commands is keyed by the command name (without leading slash).
	Commands map[string]RegisteredCommand

	// CommandOrder records command registration order. Go maps do not retain
	// insertion order; loaders populate this alongside Commands so resolved
	// invocation names match upstream extension and registration order.
	CommandOrder []string

	// Flags is keyed by the flag name.
	Flags map[string]ExtensionFlag

	// FlagOrder records the first registration of each flag name. Loaders populate it alongside Flags, so --help lists flags in upstream registration order.
	FlagOrder []string

	// Shortcuts is keyed by the canonical KeyID (e.g. "ctrl+shift+r").
	Shortcuts map[KeyID]ExtensionShortcut

	// Hidden omits the extension from the startup Extensions list.
	// upstream: .upstream/v0.99.1/packages/coding-agent/src/core/extensions/types.ts:2208 (Extension.hidden)
	Hidden bool
}

// HandlerFn is the type-erased storage shape for event handlers.
//
// The typed API methods (`OnToolCall`, `OnSessionStart`, ...) accept
// strongly-typed handler functions and wrap them into HandlerFn for
// storage in `Extension.Handlers`. The Runner reverses the wrap when
// dispatching: for each event firing, it looks up handlers by event
// name, calls each with the typed payload boxed as `any`, and casts the
// returned `any` back to the typed *EventResult shape.
//
// This is the implementation detail of D1, which uses type erasure where Go
// cannot express upstream's generic event-handler map. Typed API methods and SDK
// helpers keep HandlerFn out of normal extension authoring.
//
// upstream: types.ts:1380 (type HandlerFn = (...args: unknown[]) => Promise<unknown>)
type HandlerFn = func(args ...any) (any, error)

type registeredEventHandler struct {
	id      int
	handler HandlerFn
}

type eventHandlerState struct {
	mu       sync.RWMutex
	handlers map[string][]registeredEventHandler
}

// InitializeEventHandlers imports legacy Handlers once. Call before sharing an
// Extension between host and runner copies.
func (e *Extension) InitializeEventHandlers() {
	if e.handlerState != nil {
		return
	}
	state := &eventHandlerState{handlers: make(map[string][]registeredEventHandler, len(e.Handlers))}
	for event, handlers := range e.Handlers {
		for _, handler := range handlers {
			state.handlers[event] = append(state.handlers[event], registeredEventHandler{handler: handler})
		}
	}
	e.handlerState = state
}

// AddEventHandler appends one identity-bearing handler in registration order.
func (e *Extension) AddEventHandler(event string, id int, handler HandlerFn) {
	e.InitializeEventHandlers()
	e.handlerState.mu.Lock()
	defer e.handlerState.mu.Unlock()
	e.handlerState.handlers[event] = append(e.handlerState.handlers[event], registeredEventHandler{id: id, handler: handler})
	if e.Handlers == nil {
		e.Handlers = make(map[string][]HandlerFn)
	}
	e.Handlers[event] = append(e.Handlers[event], handler)
}

// RemoveEventHandler removes the exact registered identity. It is idempotent.
func (e *Extension) RemoveEventHandler(event string, id int) {
	if e.handlerState == nil {
		return
	}
	e.handlerState.mu.Lock()
	defer e.handlerState.mu.Unlock()
	handlers := e.handlerState.handlers[event]
	for i, handler := range handlers {
		if handler.id != id {
			continue
		}
		handlers = append(handlers[:i:i], handlers[i+1:]...)
		legacy := e.Handlers[event]
		if i < len(legacy) {
			legacy = append(legacy[:i:i], legacy[i+1:]...)
			if len(legacy) == 0 {
				delete(e.Handlers, event)
			} else {
				e.Handlers[event] = legacy
			}
		}
		if len(handlers) == 0 {
			delete(e.handlerState.handlers, event)
		} else {
			e.handlerState.handlers[event] = handlers
		}
		return
	}
}

// ReplaceEventHandlers makes e's handlers exactly source's, in place, so every
// copy of e that shares its registry dispatches to the new set from the next
// dispatch on. A restarted subprocess registers afresh; its runner keeps the
// copy it was built with.
func (e *Extension) ReplaceEventHandlers(source *Extension) {
	source.InitializeEventHandlers()
	e.InitializeEventHandlers()
	source.handlerState.mu.RLock()
	handlers := make(map[string][]registeredEventHandler, len(source.handlerState.handlers))
	legacy := make(map[string][]HandlerFn, len(source.handlerState.handlers))
	for event, registered := range source.handlerState.handlers {
		handlers[event] = append([]registeredEventHandler(nil), registered...)
		for _, handler := range registered {
			legacy[event] = append(legacy[event], handler.handler)
		}
	}
	source.handlerState.mu.RUnlock()

	e.handlerState.mu.Lock()
	defer e.handlerState.mu.Unlock()
	e.handlerState.handlers = handlers
	if e.Handlers == nil {
		e.Handlers = legacy
		return
	}
	clear(e.Handlers)
	maps.Copy(e.Handlers, legacy)
}

// EventHandlers returns a stable dispatch snapshot. Registration changes made
// while it is being iterated apply only to the next dispatch.
func (e *Extension) EventHandlers(event string) []HandlerFn {
	if e.handlerState == nil {
		handlers := e.Handlers[event]
		return append([]HandlerFn(nil), handlers...)
	}
	e.handlerState.mu.RLock()
	defer e.handlerState.mu.RUnlock()
	registered := e.handlerState.handlers[event]
	handlers := make([]HandlerFn, len(registered))
	for i, handler := range registered {
		handlers[i] = handler.handler
	}
	return handlers
}
