// SPDX-License-Identifier: MIT

// Package abi is the Durable core host ABI 1 (docs/plan/durable-core/ABI.md): the event, step and value types, their
// wire codecs, the step builder every core package emits into, and ID, the abi_id every build exports.
//
// The normative tables live in package spec, which computes ID; spec's test fails when ID here is stale
// (regenerate with go generate ./durable/core/abi). This package is part of the core: no reflection, no
// encoding/json, no fmt, no package-level mutable state.
package abi

// EventKind is the kind of an event a host sends with step (ABI section 5).
type EventKind uint8

// Event kinds.
const (
	EventOpen EventKind = iota + 1
	EventRows
	EventSubmit
	EventModelEvent
	EventModelBytes
	EventToolProgress
	EventToolDone
	EventHookDone
	EventPhaseDone
	EventTx
	EventTimer
	EventAbort
	EventRegistry
	EventAPI
	EventClose
	EventInspect
)

// EffectKind is the kind of an effect a step asks the host to start (ABI section 6).
type EffectKind uint8

// Effect kinds.
const (
	EffectTimer EffectKind = iota + 1
	EffectTimerClear
	EffectModelContext
	EffectModelHTTP
	EffectCancel
	EffectTool
	EffectHook
	EffectPhase
	EffectEnv
	EffectLiveness
	EffectSection
	EffectDeferred
	EffectCallback
)

// NoticeKind is the kind of a notice a step publishes (ABI section 7).
type NoticeKind uint8

// Notice kinds.
const (
	NoticePublished NoticeKind = iota + 1
	NoticeAPIResult
	NoticeTxResult
	NoticeReport
	NoticeSidecar
	NoticeProgressAck
)

// Status is the outcome of a step (ABI section 4.4).
type Status uint8

// Statuses.
const (
	StatusOK Status = iota
	StatusRejected
	StatusFatal
)

// ValueKind is the tag of a bound or returned value (ABI section 4.3).
type ValueKind uint8

// Value tags.
const (
	ValueNull ValueKind = iota
	ValueInt
	ValueFloat
	ValueText
	ValueBlob
)

// Phase values of a model_bytes event (ABI section 5).
const (
	ModelBytesHead uint8 = iota
	ModelBytesChunk
	ModelBytesEnd
	ModelBytesError
)

// Kind kinds of a tool_progress event (ABI section 5).
const (
	ToolProgressOutput uint8 = iota
	ToolProgressDetails
	ToolProgressDiagnostic
)

// Outcomes of tool_done, hook_done and phase_done events.
const (
	OutcomeResult uint8 = iota
	OutcomeThrown
)
