// Ports packages/durable/src/codemode/codemode.ts (the documents and the stored shapes).

package codemode

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/durable"
)

// ModuleVersion is the version of everything in this package that turns stored results into what a script sees: result
// mapping, globals, start data. Bump it when that changes, so a call started before an upgrade is not replayed by
// different code.
const ModuleVersion = 2

// ToolName is the name of the code mode tool.
const ToolName = "codemode"

// declaredTool is one tool as the script sees it: `tools.<jsName>`. Structured: the tool declares a structured output
// schema, so an error result with a value resolves with it. Hidden: an internal tool of this extension (`pi.codemode.*`),
// which scripts reach only through a global such as `models.classify()`; never in `tools`, `ALL_TOOLS`, or tool search.
type declaredTool struct {
	Name        string `json:"name"`
	JSName      string `json:"jsName"`
	Description string `json:"description"`
	Structured  bool   `json:"structured,omitempty"`
	Hidden      bool   `json:"hidden,omitempty"`
}

// startNamespace is a namespace of the tools, as `describeNamespace()` and `searchTools()` see it.
type startNamespace struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	Tools        []string `json:"tools"`
}

// declaredGlobal is one global a script may call.
type declaredGlobal struct {
	Name   string `json:"name"`
	Spread bool   `json:"spread"`
}

// start is what a call starts with; a rerun starts the VM from exactly this.
type start struct {
	Version   int     `json:"version"`
	StartedAt float64 `json:"startedAt"`
	Seed      uint32  `json:"seed"`
	// Fingerprint is the VM's fingerprint, recorded by the first run before any call is admitted.
	Fingerprint *string `json:"fingerprint,omitempty"`
	// Deadline is `startedAt + timeout_ms` when the script set one.
	Deadline *float64       `json:"deadline,omitempty"`
	Tools    []declaredTool `json:"tools"`
	// Namespaces are the namespaces of the tools, as `describeNamespace()` and `searchTools()` see them.
	Namespaces []startNamespace `json:"namespaces"`
	Globals    []declaredGlobal `json:"globals"`
	// MaxOutputTokens is `max_output_tokens` of the script's options line, or the default.
	MaxOutputTokens int                        `json:"maxOutputTokens"`
	Store           map[string]json.RawMessage `json:"store"`
}

// runState is the content of the run document.
type runState struct {
	Start *start `json:"start,omitempty"`
	// OutputDir is the directory of the result's files (the full output, images), created once: a rerun writes the same
	// files again.
	OutputDir *string `json:"outputDir,omitempty"`
	// Result is the outcome, decided once.
	Result *storedResult `json:"result,omitempty"`
}

// RunDoc is the pi.codemode.run document of a code mode call's task: the start data, written once before the VM starts,
// and the outcome, written once when the call decides it (with the store writes, in one transaction), so a rerun after
// that returns it without a VM.
var RunDoc = durable.DefineDoc(durable.DocDefinition[runState]{
	CommonDocDefinition: durable.CommonDocDefinition[runState]{Kind: "pi.codemode.run", Version: 1, Initial: func() runState { return runState{} }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeTask},
})

// outcomeJSON is an Outcome as the journal stores it: a global's outcome, which nothing else stores.
type outcomeJSON struct {
	Ok         bool            `json:"ok"`
	Value      json.RawMessage `json:"value,omitempty"`
	Message    string          `json:"message,omitempty"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

// journalGlobal is a global's call and outcome; a tool's result is its nested call's.
type journalGlobal struct {
	Name    string          `json:"name"`
	Args    json.RawMessage `json:"args,omitempty"`
	Outcome outcomeJSON     `json:"outcome"`
}

// journalEntry is one delivered result, in delivery order. Its result number (seq) is its position + 1.
type journalEntry struct {
	// ID is the VM's call ID.
	ID int `json:"id"`
	// At is the VM clock while it processes this result.
	At float64 `json:"at"`
	// IssuedAfter is the number of calls the VM had issued after processing this result; absent until acknowledged.
	IssuedAfter *int           `json:"issuedAfter,omitempty"`
	Global      *journalGlobal `json:"global,omitempty"`
}

type journalState struct {
	Entries []journalEntry `json:"entries"`
	// FirstIssued is the number of calls the VM had issued after the script's first run, before any result; absent until
	// acknowledged.
	FirstIssued *int `json:"firstIssued,omitempty"`
}

// JournalDoc is the pi.codemode.journal document of a code mode call's task: one entry per delivered result, in delivery
// order: the VM call ID, the clock value, the calls the VM had issued after processing it (once acknowledged), and a
// global's outcome.
var JournalDoc = durable.DefineDoc(durable.DocDefinition[journalState]{
	CommonDocDefinition: durable.CommonDocDefinition[journalState]{Kind: "pi.codemode.journal", Version: 1, Initial: func() journalState { return journalState{Entries: []journalEntry{}} }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeTask},
})

// StoreState is what scripts keep with `store()` and read with `load()`.
type StoreState struct {
	Values map[string]json.RawMessage `json:"values"`
}

// StoreDoc is the pi.codemode.store document of a conversation: rewindable and copied as of the fork point, so a fork
// sees the values as they were when its entry was committed.
var StoreDoc = durable.DefineDoc(durable.DocDefinition[StoreState]{
	CommonDocDefinition: durable.CommonDocDefinition[StoreState]{Kind: "pi.codemode.store", Version: 1, Initial: func() StoreState { return StoreState{Values: map[string]json.RawMessage{}} }},
	DocumentSemantics:   durable.NewRewindableConversationSemantics(durable.ForkAsOf).DocumentSemantics,
})

// NestedCall is one tool call a script made, as the code mode call's details show it.
type NestedCall struct {
	// ID is the VM's number for the call; its nested call ID is `<callId>/c<id>`.
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Args is compact JSON of the arguments, cut for display.
	Args string `json:"args"`
	// Status is running, ok, error or cancelled.
	Status     string `json:"status"`
	DurationMs *int64 `json:"durationMs,omitempty"`
	// Error is the error text, cut for display.
	Error string `json:"error,omitempty"`
}

// ToolDetails are the code mode call's details, in its result: its tool calls in the order it made them, with their
// status. Calls to globals are not listed. While the script runs, UIs show its nested calls from `pi.live.nestedTools`,
// so the list is written once, with the result.
type ToolDetails struct {
	Calls []NestedCall `json:"calls"`
	// FullOutputPath is the temp file with the complete text output, when the output was cut to its token budget.
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}
