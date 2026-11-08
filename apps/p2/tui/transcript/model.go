// Package transcript is the pure display model of a Durable view (src/web/transcript.ts): tool calls are joined with their live slot and result entry,
// so one card follows a call from request to result. Lane E ports it for the TUI (the browser keeps its TypeScript copy).
//
// pi: apps/p2/src/web/transcript.ts (types; the builder is in build.go)
package transcript

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// ToolStatus is a tool call's status: "pending", "running", "done", "error" or "interrupted".
type ToolStatus string

// The ToolStatus values.
const (
	ToolPending     ToolStatus = "pending"
	ToolRunning     ToolStatus = "running"
	ToolDone        ToolStatus = "done"
	ToolError       ToolStatus = "error"
	ToolInterrupted ToolStatus = "interrupted"
)

// ToolView is a tool call joined with its live slot and result entry.
type ToolView struct {
	ID     string
	Name   string
	Args   *jsvalue.Object
	Status ToolStatus
	Output string
	// Details is the result's details (a JSON value); nil when absent.
	Details any
	Images  []ai.ImageContent
	// Calls are the calls this call made to other tools, in creation order: Durable's live nested slots, or the last ones this browser saw. The tool's
	// contribution may give its own list from the result (`ToolActivity.nestedCalls`). Nil is `undefined`; an empty non-nil slice is an empty list.
	Calls []NestedCall
	// DurationMs is the result's recorded execution time; nil when none was recorded.
	DurationMs *float64
}

// NestedCall is one call a running tool made to another tool, as a status row.
type NestedCall struct {
	ID   string
	Name string
	// Args are the arguments; ArgsText is their JSON text when a settled list cut it too short to parse (then Args is nil).
	Args     *jsvalue.Object
	ArgsText string
	Status   ToolStatus
	// Depth is 0 for calls of the model's call, 1 for their own nested calls, and so on.
	Depth int
	// Output is the running output, while running.
	Output     string
	DurationMs *float64
	Error      string
}

// ConversationPeer is the conversation a message came from.
type ConversationPeer struct{ ID, Title string }

// AgentRef is a subagent's message: its report, a question or a note, from the entry's data.
type AgentRef struct{ From, Intent string }

// ReplyQuote is a reply's quoted line: whose message it answers and the start of that message.
type ReplyQuote struct {
	EntryID int
	Name    string
	Excerpt string
}

// Item is one display item of a transcript: a UserItem, AssistantItem, FailureItem, MarkerItem, ToolItem or GapItem.
type Item interface {
	// ItemKey is the item's stable key.
	ItemKey() string
	isItem()
}

// UserItem is a person's message.
type UserItem struct {
	Key    string
	Text   string
	Author *runtime.Author
	Peer   *ConversationPeer
	// Agent is set for a subagent's message.
	Agent  *AgentRef
	Images []ai.ImageContent
	Files  []string
	// EntryID is the entry this item shows; nil for queued items.
	EntryID *int
	// At is when it was sent (its model message's time), in ms; nil for display-only entries.
	At *int64
	// Chat marks a display-only entry shown as a person's message (it never reached the model).
	Chat bool
	// ReplyTo is the message this one answers: shown as a quoted line that leads to it.
	ReplyTo *ReplyQuote
	// Note is a plugin's short note about the message, e.g. how much chat a mention passed on.
	Note string
}

// AssistantItem is an assistant message with its tool calls.
type AssistantItem struct {
	Key string
	// EntryID is the committed entry; nil while streaming.
	EntryID   *int
	Message   *ai.AssistantMessage
	Tools     []ToolView
	Streaming bool
}

// FailureItem is a run that failed.
type FailureItem struct{ Key, Text string }

// MarkerItem is a marker line with an optional detail.
type MarkerItem struct{ Key, Label, Detail string }

// ToolItem is a tool call with no assistant message of its own.
type ToolItem struct {
	Key  string
	Tool ToolView
}

// GapItem marks entries between two loaded ones that are missing (`TranscriptStore.fillGap`); Loading while they load.
type GapItem struct {
	Key     string
	Gap     harness.TranscriptGap
	Loading bool
}

func (i UserItem) ItemKey() string      { return i.Key }
func (i AssistantItem) ItemKey() string { return i.Key }
func (i FailureItem) ItemKey() string   { return i.Key }
func (i MarkerItem) ItemKey() string    { return i.Key }
func (i ToolItem) ItemKey() string      { return i.Key }
func (i GapItem) ItemKey() string       { return i.Key }
func (UserItem) isItem()                {}
func (AssistantItem) isItem()           {}
func (FailureItem) isItem()             {}
func (MarkerItem) isItem()              {}
func (ToolItem) isItem()                {}
func (GapItem) isItem()                 {}

// ArtifactInfo is one version of an artifact: the artifact tool's result details and the artifacts plugin's index entries.
// Lane A's runtime schemas leaf will own this type; it is replaced by that one when it lands.
type ArtifactInfo struct {
	Name    string
	Title   string
	Type    string
	Version int
}

// RetryInfo is a pending retry: when and why.
type RetryInfo struct {
	At    int64
	Error string
}

// Compaction is a running compaction.
type Compaction struct {
	ID int
	// Trigger is what started the compaction (Durable's CompactionStatus.trigger): "manual", "threshold" or "overflow".
	Trigger  string
	Blocking bool
	Attempt  int
	Retry    *RetryInfo
}

// Queued is an input waiting for the agent.
type Queued struct {
	ID   int
	Mode string
	Text string
}

// Transcript is a conversation's display items and status.
type Transcript struct {
	Items []Item
	// Earlier is true when more entries exist before the first item (`TranscriptStore.loadEarlier`).
	Earlier   bool
	Artifacts []ArtifactInfo
	Busy      bool
	Working   bool
	Retry     *RetryInfo
	// Compactions are the running compactions.
	Compactions []Compaction
	Queued      []Queued
	Agent       harness.AgentState
}
