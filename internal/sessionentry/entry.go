// Package sessionentry holds the session log's entry values (core/session-manager.ts SessionHeader, SessionEntry and the entry types).
// It imports nothing from the coding-agent packages, so the extension API can type its events with them.
package sessionentry

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

type SessionHeader struct {
	Type      string `json:"type"` // always "session"
	Version   int    `json:"version,omitempty"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
	// ParentSession holds the absolute path of the SOURCE jsonl on a
	// clone (separate JSONL with linear path-to-leaf snapshot). Empty
	// on plain Create.
	ParentSession string `json:"parentSession,omitempty"`

	// raw is the record a decoded header came from; unknown members and member order survive a rewrite of the file, as in Pi's parsed object.
	raw json.RawMessage
}

func (SessionHeader) fileEntry() {}

// SessionEntryBase is the common prefix on every non-header entry.
// `ParentID` is `*string` (not "") so we can distinguish "root entry"
// (parentId: null) from "missing field". The wire format requires
// `"parentId": null` literally for roots.
type SessionEntryBase struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`

	// raw is the record a decoded entry came from. Pi keeps the parsed object, so a member the entry type does not name, and the order of the members it does, survive a rewrite of the file.
	raw json.RawMessage
}

type MessageEntry struct {
	SessionEntryBase
	Message agent.AgentMessage `json:"message"`
}

// WithRecord is entry with raw as its record: the record a session encoded from entry, so the entry stands for it as Pi's appended object does.
func WithRecord(entry MessageEntry, raw json.RawMessage) MessageEntry {
	entry.raw = raw
	return entry
}

type ModelChangeEntry struct {
	SessionEntryBase
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// UsageEntry is model-attributed usage that does not enter LLM context, such
// as a cache-warming refresh. Mirrors upstream session-manager.ts UsageEntry.
type UsageEntry struct {
	SessionEntryBase
	// Kind is an arbitrary usage category, such as "cache_warm".
	Kind     string   `json:"kind"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Usage    ai.Usage `json:"usage"`
	// Note is an optional human-readable qualifier for usage notices.
	Note string `json:"note,omitempty"`
}

// CompactionEntry field order matches Pi's appendCompaction object literal so
// Pig-written entries serialize with the same key order.
type CompactionEntry struct {
	SessionEntryBase
	Summary          string `json:"summary"`
	FirstKeptEntryID string `json:"firstKeptEntryId"`
	TokensBefore     int    `json:"tokensBefore"`
	// Details is the value an extension or the summarizer supplied. Nil is no details member; orderedjson.Null is a member written as null.
	Details any       `json:"details,omitempty"`
	Usage   *ai.Usage `json:"usage,omitempty"`
	// UsageNull marks a nil Usage as an explicit null: an extension wrote usage: null, which JSON.stringify writes as `"usage":null`. Without it a nil Usage is left out.
	UsageNull bool `json:"-"`
	// UsageRaw is the usage object as the extension that supplied it wrote it. Pi stores the author's object (agent-session.ts compact
	// `usage = extensionCompaction.usage`, _applyBoundaryDrafts `draft.usage`), so its member order and the members Usage has no field for reach the
	// file. Usage holds the same object decoded, for the readers. Nil for a usage PiG built, which is written from Usage.
	UsageRaw json.RawMessage `json:"-"`
	FromHook bool            `json:"fromHook"`
	// FromHookAbsent leaves the fromHook member out when the entry is written: Pi's `fromHook?: boolean` left undefined by an extension's appendCompaction.
	FromHookAbsent bool `json:"-"`
	// SystemMessage is the complete prompt and tool state at this compaction
	// boundary. It is absent when the projected context has no system state.
	SystemMessage json.RawMessage `json:"systemMessage,omitempty"`
}

// UnmarshalJSON keeps the member order of `details`, the object an extension wrote (session-manager.ts appendCompaction), and a null `details` or `usage`
// apart from an absent one.
func (e *CompactionEntry) UnmarshalJSON(data []byte) error {
	type plain CompactionEntry
	if err := orderedjson.UnmarshalFields(data, (*plain)(e), "details"); err != nil {
		return err
	}
	e.UsageNull = e.Usage == nil && orderedjson.IsNullMember(data, "usage")
	return nil
}

// BranchSummaryEntry member order matches Pi's branchWithSummary object literal (session-manager.ts:1612-1622). navigateTree passes fromExtension as a
// boolean (agent-session.ts:4110-4114); a caller of SessionManager.branchWithSummary may leave fromHook undefined (FromHookAbsent).
type BranchSummaryEntry struct {
	SessionEntryBase
	FromID  string `json:"fromId"`
	Summary string `json:"summary"`
	// Details is the value an extension or the summarizer supplied. Nil is no details member; orderedjson.Null is a member written as null.
	Details any       `json:"details,omitempty"`
	Usage   *ai.Usage `json:"usage,omitempty"`
	// UsageNull marks a nil Usage as an explicit null, which JSON.stringify writes as `"usage":null`. Without it a nil Usage is left out.
	UsageNull bool `json:"-"`
	// UsageRaw is the usage object as the extension that supplied it wrote it (agent-session.ts navigateTree `summaryUsage = extensionSummary.usage`):
	// its member order and the members Usage has no field for reach the file. Nil for a usage PiG built, which is written from Usage.
	UsageRaw json.RawMessage `json:"-"`
	FromHook bool            `json:"fromHook"`

	// FromHookAbsent leaves the fromHook member out when the entry is written: Pi's `fromHook?: boolean` left undefined by a caller of
	// SessionManager.branchWithSummary (session-manager.ts:1604). A decoded entry has it set when its record has no fromHook member.
	FromHookAbsent bool `json:"-"`
}

// UnmarshalJSON keeps the member order of `details`, the object the summarizer or an extension's session_before_tree result supplied, so a
// session_tree event re-sends it as written (Pi passes the stored entry object).
func (e *BranchSummaryEntry) UnmarshalJSON(data []byte) error {
	type plain BranchSummaryEntry
	if err := orderedjson.UnmarshalFields(data, (*plain)(e), "details"); err != nil {
		return err
	}
	var probe struct {
		FromHook *bool `json:"fromHook"`
	}
	e.FromHookAbsent = json.Unmarshal(data, &probe) == nil && probe.FromHook == nil
	e.UsageNull = e.Usage == nil && orderedjson.IsNullMember(data, "usage")
	return nil
}

type CustomEntry struct {
	SessionEntryBase
	CustomType string `json:"customType"`
	// Data is the value an extension supplied. Nil is no data member (Pi's undefined); orderedjson.Null is a member written as null.
	Data any `json:"data,omitempty"`
}

type CustomMessageEntry struct {
	SessionEntryBase
	CustomType string `json:"customType"`
	// Content is a string or content blocks. Nil is no content member, which a writer that left it out gets (Pi stores undefined); orderedjson.Null is null.
	Content any  `json:"content"`
	Display bool `json:"display"`
	// DisplayAbsent leaves the display member out when the entry is written: an extension's message or draft without display, which Pi's
	// appendCustomMessageEntry stores as undefined (session-manager.ts:1350-1366). A decoded entry has it set when its record has no display member.
	DisplayAbsent bool `json:"-"`
	// DisplayNull writes the display member as null: a message or draft whose writer set display to null, which Pi stores as given. A decoded entry
	// has it set when its record holds `"display":null`. Readers take it for false.
	DisplayNull bool `json:"-"`
	// Details is the value an extension supplied. Nil is no details member; orderedjson.Null is a member written as null.
	Details any `json:"details,omitempty"`
}

// UnmarshalJSON keeps the member order of `data`, the object an extension wrote (session-manager.ts appendCustomEntry).
func (e *CustomEntry) UnmarshalJSON(data []byte) error {
	type plain CustomEntry
	return orderedjson.UnmarshalFields(data, (*plain)(e), "data")
}

// UnmarshalJSON keeps the member order of `content` blocks and `details`, the objects an extension wrote (session-manager.ts appendCustomMessageEntry).
func (e *CustomMessageEntry) UnmarshalJSON(data []byte) error {
	type plain CustomMessageEntry
	if err := orderedjson.UnmarshalFields(data, (*plain)(e), "content", "details"); err != nil {
		return err
	}
	e.DisplayAbsent = !e.Display && !orderedjson.HasMember(data, "display")
	e.DisplayNull = !e.Display && orderedjson.IsNullMember(data, "display")
	return nil
}

// LabelEntry is upstream's "user-renamed this branch" marker. Carries
// nil Label to delete a previously-set label.
type LabelEntry struct {
	SessionEntryBase
	TargetID string  `json:"targetId"`
	Label    *string `json:"label,omitempty"`
	// LabelNull marks a nil Label as an explicit null, which JSON.stringify writes as `"label":null`. Without it a nil Label is left out. Both clear the label.
	LabelNull bool `json:"-"`
}

// UnmarshalJSON keeps a null `label` apart from an absent one.
func (e *LabelEntry) UnmarshalJSON(data []byte) error {
	type plain LabelEntry
	if err := json.Unmarshal(data, (*plain)(e)); err != nil {
		return err
	}
	e.LabelNull = e.Label == nil && orderedjson.IsNullMember(data, "label")
	return nil
}

// SessionInfoEntry stores a session-level name (set via `/name`).
// Persisted as type=session_info so it survives across resumes.
type SessionInfoEntry struct {
	SessionEntryBase
	Name string `json:"name"`
}

// BashExecutionEntry persists a user shell invocation as a message with role bashExecution, matching packages/coding-agent/src/core/session-manager.ts:appendMessage.
// ExcludeFromContext retains the entry in the transcript but excludes it from LLM conversion.
type BashExecutionEntry struct {
	SessionEntryBase
	Role               string `json:"-"` // always "bashExecution"; set on read
	Command            string `json:"-"`
	Output             string `json:"-"`
	ExitCode           *int   `json:"-"`
	Cancelled          bool   `json:"-"`
	Truncated          bool   `json:"-"`
	FullOutputPath     string `json:"-"`
	FullOutputError    string `json:"-"`
	ExcludeFromContext *bool  `json:"-"` // nil when the record carries no excludeFromContext member (messages.ts:39 optional)
	MessageTimestamp   int64  `json:"-"`
}

// BashExecutionMessage is the inner `message` payload upstream uses for
// bash entries. Mirrors messages.ts:29-43 byte-for-byte.
type BashExecutionMessage struct {
	Role               string `json:"role"` // "bashExecution"
	Command            string `json:"command"`
	Output             string `json:"output"`
	ExitCode           *int   `json:"exitCode,omitempty"`
	Cancelled          bool   `json:"cancelled"`
	Truncated          bool   `json:"truncated"`
	FullOutputPath     string `json:"fullOutputPath,omitempty"`
	FullOutputError    string `json:"fullOutputError,omitempty"`
	Timestamp          int64  `json:"timestamp"`
	ExcludeFromContext *bool  `json:"excludeFromContext,omitempty"`
}

// MessageRole is "bashExecution": a BashExecutionMessage is a codingagent.SessionMessage.
func (BashExecutionMessage) MessageRole() string { return "bashExecution" }

// bashExecutionWireEntry is the on-disk wrapper (= upstream's SessionMessageEntry
// for bashExecution variants). Used solely for Marshal/Unmarshal.
type bashExecutionWireEntry struct {
	SessionEntryBase
	Message BashExecutionMessage `json:"message"`
}

// MarshalJSON serializes to upstream's wire shape (type:"message" + nested).
func (b BashExecutionEntry) MarshalJSON() ([]byte, error) {
	wire := bashExecutionWireEntry{
		Type:      "message",
		ID:        b.ID,
		ParentID:  b.ParentID,
		Timestamp: b.Timestamp,
		Message: BashExecutionMessage{
			Role:               "bashExecution",
			Command:            b.Command,
			Output:             b.Output,
			ExitCode:           b.ExitCode,
			Cancelled:          b.Cancelled,
			Truncated:          b.Truncated,
			FullOutputPath:     b.FullOutputPath,
			FullOutputError:    b.FullOutputError,
			ExcludeFromContext: b.ExcludeFromContext,
			Timestamp:          b.MessageTimestamp,
		},
	}
	return json.Marshal(wire)
}

// UnmarshalJSON accepts BOTH the new wire shape (type:"message" with
// inner role:"bashExecution") AND the legacy pig shape (type:"bash_execution"
// with flat top-level fields), so older session files still load.
func (b *BashExecutionEntry) UnmarshalJSON(data []byte) error {
	// Peek base to decide which path.
	var probe struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if probe.Type == "message" && len(probe.Message) > 0 {
		// New shape (upstream parity).
		var wire bashExecutionWireEntry
		if err := json.Unmarshal(data, &wire); err != nil {
			return err
		}
		b.SessionEntryBase = wire.SessionEntryBase
		b.Role = wire.Message.Role
		b.Command = wire.Message.Command
		b.Output = wire.Message.Output
		b.ExitCode = wire.Message.ExitCode
		b.Cancelled = wire.Message.Cancelled
		b.Truncated = wire.Message.Truncated
		b.FullOutputPath = wire.Message.FullOutputPath
		b.FullOutputError = wire.Message.FullOutputError
		b.ExcludeFromContext = wire.Message.ExcludeFromContext
		b.MessageTimestamp = wire.Message.Timestamp
		return nil
	}
	// Legacy PiG shape with top-level fields.
	var legacy struct {
		SessionEntryBase
		Role               string `json:"role"`
		Command            string `json:"command"`
		Output             string `json:"output"`
		ExitCode           *int   `json:"exitCode"`
		Cancelled          bool   `json:"cancelled"`
		Truncated          bool   `json:"truncated"`
		FullOutputPath     string `json:"fullOutputPath,omitempty"`
		FullOutputError    string `json:"fullOutputError,omitempty"`
		ExcludeFromContext *bool  `json:"excludeFromContext,omitempty"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	b.SessionEntryBase = legacy.SessionEntryBase
	b.Type = "bash_execution"
	b.Role = legacy.Role
	b.Command = legacy.Command
	b.Output = legacy.Output
	b.ExitCode = legacy.ExitCode
	b.Cancelled = legacy.Cancelled
	b.Truncated = legacy.Truncated
	b.FullOutputPath = legacy.FullOutputPath
	b.FullOutputError = legacy.FullOutputError
	b.ExcludeFromContext = legacy.ExcludeFromContext
	return nil
}

// ThinkingLevelEntry records a user-initiated thinking level change (Shift+Tab).
// Mirrors upstream appendThinkingLevelChange in agent-session.ts. Persisted as
// type="thinking_level_change" so the session tree can display [thinking: level].
type ThinkingLevelEntry struct {
	SessionEntryBase
	ThinkingLevel string `json:"thinkingLevel"`
}
