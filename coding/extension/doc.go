// Package extension defines the contract every pig extension speaks. It
// mirrors upstream pi's ExtensionAPI surface 1:1 so a TS extension can
// be ported to Go almost line-for-line, and so each upstream version bump is
// an additive diff rather than a structural one.
//
// Source of truth:
//
//	.upstream/current/packages/coding-agent/src/core/extensions/types.ts
//
// This package contains only the public API contract: types, the fat [API]
// interface, and the [Extension] author-facing interface. Hosts (the inproc
// dispatch runner and subprocess transport adapter) live under
// coding/extension/host/ and depend on this package, never the reverse.
//
// # Faithfulness rules
//
//   - Method order on the [API] interface matches upstream ExtensionAPI
//     declaration order. Section dividers mirror upstream comments.
//   - Every field on every event struct carries a json:"camelCaseName" tag
//     matching upstream's TS field name. The parity gates in
//     tests/upstream-parity/ enforce this on every CI run.
//   - Field names use idiomatic Go casing with terminal initialism uplift
//     (Id→ID, Url→URL, Api→API, Json→JSON). For example, upstream
//     `toolCallId` becomes Go `ToolCallID`. The parity reflection check
//     uses [parity.CamelToGoField] which knows the closed initialism set;
//     extending it requires a coordinated update to that helper plus a
//     DIVERGENCES.md U1 sync-ritual entry.
//   - Observable differences use a numbered source marker and ledger record.
//
// # Opaque compatibility types
//
// Upstream references many domain types (AgentMessage, Model, CompactionEntry,
// AbortSignal, Component, etc.) that don't yet have concrete Go equivalents
// in PiG. Types that cross the current dynamic JSON or renderer boundaries are
// explicit opaque aliases in opaque_types.go. Concrete events and SDK methods
// use typed Go values where the contract is stable.
package extension
