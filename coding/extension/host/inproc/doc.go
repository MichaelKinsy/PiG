// Package inproc implements the in-process Go dispatch runner for extensions.
//
// Architecture. Extensions are loaded as pre-populated `extension.Extension`
// state containers (the loader, not this package, runs the factory
// functions and populates the maps). A Runner wraps `[]Extension` and:
//
//   - Dispatches events to typed handlers (the hot path).
//   - Aggregates registered tools, commands, shortcuts, flags, and
//     renderers from across all loaded extensions for the agent core
//     and TUI to consume.
//   - Owns the staleness lifecycle: on /reload or session replacement,
//     the host calls `Runner.Invalidate(message)` and any subsequent
//     API call captured by a context bound to this runner returns
//     ErrStaleContext. This mirrors upstream `runner.ts:461-471`.
//
// Scope. This package owns the parity dispatch engine: stale-state
// lifecycle, resource aggregation, event dispatch, handler errors,
// providers, and resource diagnostics. Loading and transport adaptation
// live outside this package.
//
// Sync compatibility. Field names and method names mirror upstream
// `runner.ts` private/public names verbatim where the Go semantics
// permit. Each exported method's doc comment cites
// `// upstream: runner.ts:NNN`. The atomic.Pointer used for the stale
// flag is a TS→Go translation rule (DIVERGENCES.md), not a divergence.
//
// upstream: .upstream/current/packages/coding-agent/src/core/extensions/runner.ts
package inproc
