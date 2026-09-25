// Package session implements the durable harness session: the write-once
// entry tree, bound values and lists, the usage ledger, the storage contract,
// the Session mutation line, Branch capabilities, forks, and the in-memory
// backend.
package session

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

// JsonValue is a strict JSON value.
type JsonValue = harness.JsonValue

// SettledAssistantMessage is an assistant message whose stopReason is not
// "pending"; session writes reject pending assistant messages.
type SettledAssistantMessage = agent.AssistantMessage

// EntryType is the structural type of one entry.
type EntryType string

const (
	EntryTypeMessage       EntryType = "message"
	EntryTypeCompaction    EntryType = "compaction"
	EntryTypeBranchSummary EntryType = "branch_summary"
	EntryTypeCustom        EntryType = "custom"
)

// Entry is one write-once conversation record: placement fields plus the
// payload of its Type. Fields of other entry types are ignored. As a
// transaction input (upstream NewEntry) Seq and Timestamp are unset; storage
// assigns them at commit.
type Entry struct {
	ID         string
	ParentID   *string
	Seq        int64
	Timestamp  int64
	Type       EntryType
	CustomType string

	// Message entries.
	Message agent.AgentMessage
	// Terminate marks a tool result that ends the run (upstream terminate?: true).
	Terminate bool

	// Compaction and branch-summary entries.
	Summary      string
	RetainedTail []agent.AgentMessage
	TokensBefore int
	Details      *JsonValue
	Usage        *ai.Usage
	FromHook     bool
	FromID       *string

	// Custom entries; nil means the entry has no data.
	Data *JsonValue
}

type entryWire struct {
	ID           string               `json:"id"`
	ParentID     *string              `json:"parentId"`
	Seq          int64                `json:"seq"`
	Timestamp    int64                `json:"timestamp"`
	Type         EntryType            `json:"type"`
	CustomType   string               `json:"customType,omitempty"`
	Message      *agent.AgentMessage  `json:"message,omitempty"`
	Terminate    bool                 `json:"terminate,omitempty"`
	Summary      string               `json:"summary"`
	RetainedTail []agent.AgentMessage `json:"retainedTail"`
	TokensBefore int                  `json:"tokensBefore"`
	Details      *JsonValue           `json:"details,omitempty"`
	Usage        *ai.Usage            `json:"usage,omitempty"`
	FromHook     bool                 `json:"fromHook"`
	FromID       *string              `json:"fromId"`
	Data         *JsonValue           `json:"data,omitempty"`
}

var entryKeys = map[EntryType][]string{
	EntryTypeMessage:       {"id", "parentId", "type", "message", "terminate", "seq", "timestamp"},
	EntryTypeCompaction:    {"id", "parentId", "type", "summary", "retainedTail", "tokensBefore", "details", "usage", "fromHook", "seq", "timestamp"},
	EntryTypeBranchSummary: {"id", "parentId", "type", "fromId", "summary", "details", "usage", "fromHook", "seq", "timestamp"},
	EntryTypeCustom:        {"id", "parentId", "type", "customType", "data", "seq", "timestamp"},
}

// MarshalJSON emits the upstream entry shape for Type.
func (entry Entry) MarshalJSON() ([]byte, error) {
	return entry.marshal(true)
}

// marshal emits the entry, without seq and timestamp for a transaction input.
func (entry Entry) marshal(placed bool) ([]byte, error) {
	keys, ok := entryKeys[entry.Type]
	if !ok {
		return nil, fmt.Errorf("unknown entry type %q", entry.Type)
	}
	if !placed {
		keys = keys[:len(keys)-2]
	}
	wire := entryWire{
		ID: entry.ID, ParentID: entry.ParentID, Seq: entry.Seq, Timestamp: entry.Timestamp,
		Type: entry.Type, CustomType: entry.CustomType, Terminate: entry.Terminate,
		Summary: entry.Summary, RetainedTail: entry.RetainedTail, TokensBefore: entry.TokensBefore,
		Details: entry.Details, Usage: entry.Usage, FromHook: entry.FromHook, FromID: entry.FromID, Data: entry.Data,
	}
	if entry.Type == EntryTypeMessage {
		wire.Message = &entry.Message
	}
	if entry.Type == EntryTypeCompaction && wire.RetainedTail == nil {
		wire.RetainedTail = []agent.AgentMessage{}
	}
	return marshalOrdered(wire, keys)
}

// UnmarshalJSON decodes any entry type, keeping explicit null details/data
// distinct from absent members.
func (entry *Entry) UnmarshalJSON(data []byte) error {
	var wire entryWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if _, ok := entryKeys[wire.Type]; !ok {
		return fmt.Errorf("unknown entry type %q", wire.Type)
	}
	fields, err := rawFields(data)
	if err != nil {
		return err
	}
	details, err := optionalJSON(fields, "details")
	if err != nil {
		return err
	}
	payload, err := optionalJSON(fields, "data")
	if err != nil {
		return err
	}
	decoded := Entry{
		ID: wire.ID, ParentID: wire.ParentID, Seq: wire.Seq, Timestamp: wire.Timestamp, Type: wire.Type,
		CustomType: wire.CustomType, Terminate: wire.Terminate, Summary: wire.Summary,
		RetainedTail: wire.RetainedTail, TokensBefore: wire.TokensBefore, Details: details, Usage: wire.Usage,
		FromHook: wire.FromHook, FromID: wire.FromID, Data: payload,
	}
	if wire.Message != nil {
		decoded.Message = *wire.Message
	}
	*entry = decoded
	return nil
}

// EntryProjector converts an application-defined custom entry into model
// context; nil messages project nothing.
type EntryProjector func(ctx context.Context, entry Entry) ([]agent.AgentMessage, error)

// ModelRef identifies a provider model durably.
type ModelRef struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// LaneConfiguration is the total configuration of one AgentLane.
type LaneConfiguration struct {
	Model           ModelRef         `json:"model"`
	ThinkingLevel   ai.ThinkingLevel `json:"thinkingLevel"`
	ActiveToolNames []string         `json:"activeToolNames"`
}

// MarshalJSON keeps an empty tool list as [].
func (configuration LaneConfiguration) MarshalJSON() ([]byte, error) {
	type plain LaneConfiguration
	configuration.ActiveToolNames = stringsOrEmpty(configuration.ActiveToolNames)
	return json.Marshal(plain(configuration))
}

// UsageRow is one append-only cost ledger row.
type UsageRow struct {
	ID    string   `json:"id"`
	Seq   int64    `json:"seq"`
	Usage ai.Usage `json:"usage"`
	// EntryID names the entry the cost belongs to, when there is one.
	EntryID *string `json:"entryId,omitempty"`
	// Adjustment marks a caller-supplied reconciliation row.
	Adjustment bool       `json:"adjustment"`
	Details    *JsonValue `json:"details,omitempty"`
}

// UnmarshalJSON keeps explicit null details distinct from absent details.
func (row *UsageRow) UnmarshalJSON(data []byte) error {
	type plain UsageRow
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	fields, err := rawFields(data)
	if err != nil {
		return err
	}
	if decoded.Details, err = optionalJSON(fields, "details"); err != nil {
		return err
	}
	*row = UsageRow(decoded)
	return nil
}

// SessionStats are the maintained message-count and ledger totals.
type SessionStats struct {
	MessageCount int      `json:"messageCount"`
	Usage        ai.Usage `json:"usage"`
}

// CommitResult reports the sequences and timestamp one commit assigned and the
// session totals immediately after it applied.
type CommitResult struct {
	FirstSeq  int64        `json:"firstSeq"`
	Seqs      []int64      `json:"seqs"`
	Timestamp int64        `json:"timestamp"`
	Stats     SessionStats `json:"stats"`
}

// EntryStructure is an entry without payload fields.
type EntryStructure struct {
	ID         string    `json:"id"`
	ParentID   *string   `json:"parentId"`
	Seq        int64     `json:"seq"`
	Timestamp  int64     `json:"timestamp"`
	Type       EntryType `json:"type"`
	CustomType string    `json:"customType,omitempty"`
}

// EntryCursor is an exclusive sequence cursor.
type EntryCursor struct {
	Seq int64 `json:"seq"`
}

// Branch scan orders.
const (
	OrderNewestFirst = "newestFirst"
	OrderOldestFirst = "oldestFirst"
	OrderAsc         = "asc"
	OrderDesc        = "desc"
)

// BranchScan queries the path from Start (default: the receiver's tip) toward
// the root. Empty strings and nil pointers mean the member is absent.
type BranchScan struct {
	Start      *string
	StopAtType EntryType
	StopAtID   string
	Type       EntryType
	CustomType string
	// Order is OrderNewestFirst (default) or OrderOldestFirst.
	Order  string
	Limit  *int
	Cursor *EntryCursor
}

// StorageBranchScan is a BranchScan with a required start.
type StorageBranchScan struct {
	Start      string
	StopAtType EntryType
	StopAtID   string
	Type       EntryType
	CustomType string
	Order      string
	Limit      *int
	Cursor     *EntryCursor
}

// EntryScan queries the session-wide entry inventory.
type EntryScan struct {
	Type       EntryType
	CustomType string
	FromSeq    *int64
	ToSeq      *int64
	// Order is OrderAsc (default) or OrderDesc.
	Order string
	Limit *int
}

// UsageScan queries the usage ledger.
type UsageScan struct {
	FromSeq *int64
	ToSeq   *int64
	Order   string
	Limit   *int
}

// EntryQuery queries session-wide entries; Order defaults to OrderDesc.
type EntryQuery struct {
	Type       EntryType
	CustomType string
	Order      string
	Limit      *int
	Cursor     *EntryCursor
}

// SessionMetadata describes one stored session. The upstream per-backend
// metadata subtypes flatten into optional members: Cwd, Path, and ModifiedAt
// are populated by file-backed repositories.
type SessionMetadata struct {
	ID                      string `json:"id"`
	CreatedAt               int64  `json:"createdAt"`
	StorageVersion          int    `json:"storageVersion"`
	Cwd                     string `json:"cwd,omitempty"`
	ParentSessionID         string `json:"parentSessionId,omitempty"`
	LegacyParentSessionPath string `json:"legacyParentSessionPath,omitempty"`
	Path                    string `json:"path,omitempty"`
	// ModifiedAt is the filesystem modification time in Unix milliseconds.
	ModifiedAt int64 `json:"modifiedAt,omitempty"`
}

// SessionCreateOptions configure repository creation; empty strings are
// absent. Cwd is required by file-backed repositories.
type SessionCreateOptions struct {
	ID              string
	ParentSessionID string
	Cwd             string
}

// Fork scopes.
const (
	ForkScopeBranch = "branch"
	ForkScopeTree   = "tree"
)

// Fork positions.
const (
	ForkPositionBefore = "before"
	ForkPositionAt     = "at"
)

// ForkOptions select a repository fork. Branch scope copies one path from a
// complete configured source AgentLane; tree scope copies the whole tree and
// every Branch tip. Empty strings and nil pointers are absent.
type ForkOptions struct {
	Scope  string
	Branch string
	// EntryID must lie on the source Branch's tip ancestry; nil selects the tip.
	EntryID *string
	// Position is ForkPositionAt (default) or ForkPositionBefore.
	Position string
	ID       string
}
