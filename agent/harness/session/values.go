package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

// Address kinds.
const (
	KindValue = "value"
	KindList  = "list"
)

// StoredAddressBase is the erased (namespace, key, kind) of one durable
// location. Equal triples name the same location.
type StoredAddressBase struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Kind      string `json:"kind"`
}

// Value is a bound typed address of one replaceable durable value.
type Value[T any] struct {
	StoredAddressBase
}

// ValueList is a bound typed address of one append-only durable list.
type ValueList[T any] struct {
	StoredAddressBase
}

// Address returns the erased address.
func (address Value[T]) Address() StoredAddressBase { return address.StoredAddressBase }

// Address returns the erased address.
func (address ValueList[T]) Address() StoredAddressBase { return address.StoredAddressBase }

// StoredValue is a current value and the sequence of its last set.
type StoredValue[T any] struct {
	Address Value[T] `json:"address"`
	Value   T        `json:"value"`
	Seq     int64    `json:"seq"`
}

// ListElement is one immutable list element and its global write sequence.
type ListElement[T any] struct {
	Seq   int64 `json:"seq"`
	Value T     `json:"value"`
}

// ListCursor is an exclusive list sequence cursor.
type ListCursor struct {
	Seq int64 `json:"seq"`
}

// ListReadOptions page a list; Order defaults to OrderAsc and Limit to 1,000
// (clamped to 10,000).
type ListReadOptions struct {
	Cursor *ListCursor
	Order  string
	Limit  *int
}

// ResolvedListReadOptions are validated list read options.
type ResolvedListReadOptions struct {
	Cursor *ListCursor
	Order  string
	Limit  int
}

func validateAddress(namespace, key string) error {
	if namespace == "" {
		return errors.New("Value namespace must not be empty")
	}
	if strings.ContainsRune(namespace, 0) {
		return errors.New("Value namespace must not contain \\u0000")
	}
	if strings.ContainsRune(key, 0) {
		return errors.New("Value key must not contain \\u0000")
	}
	return nil
}

// NewValue binds a scalar address (upstream value<T>(namespace, key)).
func NewValue[T any](namespace, key string) (Value[T], error) {
	if err := validateAddress(namespace, key); err != nil {
		return Value[T]{}, err
	}
	return Value[T]{StoredAddressBase{Namespace: namespace, Key: key, Kind: KindValue}}, nil
}

// NewList binds a list address (upstream list<T>(namespace, key)).
func NewList[T any](namespace, key string) (ValueList[T], error) {
	if err := validateAddress(namespace, key); err != nil {
		return ValueList[T]{}, err
	}
	return ValueList[T]{StoredAddressBase{Namespace: namespace, Key: key, Kind: KindList}}, nil
}

// MustValue binds a scalar address and panics on an invalid component, like
// the upstream constructor's TypeError for a trusted-programming defect.
func MustValue[T any](namespace, key string) Value[T] {
	address, err := NewValue[T](namespace, key)
	if err != nil {
		panic(err)
	}
	return address
}

// MustList binds a list address and panics on an invalid component.
func MustList[T any](namespace, key string) ValueList[T] {
	address, err := NewList[T](namespace, key)
	if err != nil {
		panic(err)
	}
	return address
}

// Write is one erased transaction write: EntryWrite, UsageWrite,
// ValueSetWrite, ValueDeleteWrite, ListAppendWrite, or ListDeleteWrite.
type Write interface{ writeKind() string }

// EntryWrite inserts one complete entry.
type EntryWrite struct{ Entry Entry }

// UsageWrite inserts one ledger row; storage assigns its seq.
type UsageWrite struct{ Row UsageRow }

// ValueSetWrite replaces one scalar value.
type ValueSetWrite struct {
	Namespace string
	Key       string
	Value     any
}

// ValueDeleteWrite deletes one scalar value; deleting an absent value is a
// no-op.
type ValueDeleteWrite struct{ Namespace, Key string }

// ListAppendWrite appends one list element.
type ListAppendWrite struct {
	Namespace string
	Key       string
	Value     any
}

// ListDeleteWrite deletes a whole list; deleting an absent list is a no-op.
type ListDeleteWrite struct{ Namespace, Key string }

// MarshalJSON emits {kind: "entry", entry} with the unplaced entry.
func (write EntryWrite) MarshalJSON() ([]byte, error) {
	entry, err := write.Entry.marshal(false)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Kind  string          `json:"kind"`
		Entry json.RawMessage `json:"entry"`
	}{"entry", entry})
}

// MarshalJSON emits {kind: "usage", row} without the unassigned seq.
func (write UsageWrite) MarshalJSON() ([]byte, error) {
	row, err := marshalOrdered(write.Row, []string{"id", "usage", "entryId", "adjustment", "details"})
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Kind string          `json:"kind"`
		Row  json.RawMessage `json:"row"`
	}{"usage", row})
}

type addressWrite struct {
	Kind      string `json:"kind"`
	Op        string `json:"op"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     any    `json:"value"`
}

func marshalAddressWrite(kind, op, namespace, key string, value any, withValue bool) ([]byte, error) {
	keys := []string{"kind", "op", "namespace", "key"}
	if withValue {
		keys = append(keys, "value")
	}
	return marshalOrdered(addressWrite{kind, op, namespace, key, value}, keys)
}

// MarshalJSON emits {kind: "value", op: "set", namespace, key, value}.
func (write ValueSetWrite) MarshalJSON() ([]byte, error) {
	return marshalAddressWrite(KindValue, "set", write.Namespace, write.Key, write.Value, true)
}

// MarshalJSON emits {kind: "value", op: "delete", namespace, key}.
func (write ValueDeleteWrite) MarshalJSON() ([]byte, error) {
	return marshalAddressWrite(KindValue, "delete", write.Namespace, write.Key, nil, false)
}

// MarshalJSON emits {kind: "list", op: "append", namespace, key, value}.
func (write ListAppendWrite) MarshalJSON() ([]byte, error) {
	return marshalAddressWrite(KindList, "append", write.Namespace, write.Key, write.Value, true)
}

// MarshalJSON emits {kind: "list", op: "delete", namespace, key}.
func (write ListDeleteWrite) MarshalJSON() ([]byte, error) {
	return marshalAddressWrite(KindList, "delete", write.Namespace, write.Key, nil, false)
}

func (EntryWrite) writeKind() string       { return "entry" }
func (UsageWrite) writeKind() string       { return "usage" }
func (ValueSetWrite) writeKind() string    { return KindValue }
func (ValueDeleteWrite) writeKind() string { return KindValue }
func (ListAppendWrite) writeKind() string  { return KindList }
func (ListDeleteWrite) writeKind() string  { return KindList }

// SetValue constructs a scalar set write whose value type is fixed by address.
func SetValue[T any](address Value[T], next T) ValueSetWrite {
	return ValueSetWrite{Namespace: address.Namespace, Key: address.Key, Value: next}
}

// DeleteValue constructs a scalar delete write.
func DeleteValue[T any](address Value[T]) ValueDeleteWrite {
	return ValueDeleteWrite{Namespace: address.Namespace, Key: address.Key}
}

// AppendList constructs a list append write whose element type is fixed by
// address.
func AppendList[T any](address ValueList[T], element T) ListAppendWrite {
	return ListAppendWrite{Namespace: address.Namespace, Key: address.Key, Value: element}
}

// DeleteList constructs a whole-list delete write.
func DeleteList[T any](address ValueList[T]) ListDeleteWrite {
	return ListDeleteWrite{Namespace: address.Namespace, Key: address.Key}
}

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

// ResolveListReadOptions validates and defaults list read options.
func ResolveListReadOptions(options *ListReadOptions) (ResolvedListReadOptions, error) {
	if options == nil {
		options = &ListReadOptions{}
	}
	limit := 1_000
	if options.Limit != nil {
		limit = *options.Limit
	}
	if limit <= 0 || limit > maxSafeInteger {
		return ResolvedListReadOptions{}, errors.New("List read limit must be a positive safe integer")
	}
	order := options.Order
	if order == "" {
		order = OrderAsc
	}
	return ResolvedListReadOptions{Cursor: options.Cursor, Order: order, Limit: min(limit, 10_000)}, nil
}

// BranchTip addresses the tip of one Branch; nil is the empty root tip.
func BranchTip(branch string) Value[*string] { return MustValue[*string]("pi.branch.tip", branch) }

// BranchTipInventoryPrefix scans every Branch tip.
func BranchTipInventoryPrefix() Value[*string] { return MustValue[*string]("pi.branch.tip", "") }

// LaneConfig addresses one lane's total configuration.
func LaneConfig(lane string) Value[LaneConfiguration] {
	return MustValue[LaneConfiguration]("pi.lane.config", lane)
}

// LaneStateValue addresses one lane's state (upstream laneState; renamed
// because Go types and functions share one namespace).
func LaneStateValue(lane string) Value[LaneState] { return MustValue[LaneState]("pi.lane.state", lane) }

// OperationResult addresses one immutable terminal record.
func OperationResult(operationID string) Value[OperationResultRecord] {
	return MustValue[OperationResultRecord]("pi.result", operationID)
}

// OperationMetaValue addresses one operation's metadata (upstream
// operationMeta).
func OperationMetaValue(operationID string) Value[OperationMeta] {
	return MustValue[OperationMeta]("pi.op.meta", operationID)
}

// OperationStateValue addresses one operation's state (upstream
// operationState).
func OperationStateValue(operationID string) Value[OperationState] {
	return MustValue[OperationState]("pi.op.state", operationID)
}

// OperationToolArgs addresses one call's effective arguments.
func OperationToolArgs(operationID, stepID string, sourceIndex int) Value[map[string]JsonValue] {
	return MustValue[map[string]JsonValue]("pi.op.tool_args", operationID+":"+stepID+":"+strconv.Itoa(sourceIndex))
}

// OperationToolMemo addresses one invocation-scoped memo.
func OperationToolMemo(operationID, invocationID, name string) Value[JsonValue] {
	return MustValue[JsonValue]("pi.op.tool_memo", operationID+":"+invocationID+":"+name)
}

// OperationPreparation addresses one structural preparation.
func OperationPreparation(operationID, taskID string) Value[DurableStructuralPreparation] {
	return MustValue[DurableStructuralPreparation]("pi.op.preparation", operationID+":"+taskID)
}

// OperationToolArgsPrefix scans an operation's (or one step's) tool arguments;
// nil stepID is absent.
func OperationToolArgsPrefix(operationID string, stepID *string) Value[map[string]JsonValue] {
	key := operationID + ":"
	if stepID != nil {
		key += *stepID + ":"
	}
	return MustValue[map[string]JsonValue]("pi.op.tool_args", key)
}

// OperationToolMemoPrefix scans an operation's (or one invocation's) memos; nil invocationID is absent.
func OperationToolMemoPrefix(operationID string, invocationID *string) Value[JsonValue] {
	key := operationID + ":"
	if invocationID != nil {
		key += *invocationID + ":"
	}
	return MustValue[JsonValue]("pi.op.tool_memo", key)
}

// OperationPreparationPrefix scans an operation's preparations.
func OperationPreparationPrefix(operationID string) Value[DurableStructuralPreparation] {
	return MustValue[DurableStructuralPreparation]("pi.op.preparation", operationID+":")
}

// PendingEntryValue addresses content awaiting placement (upstream
// pendingEntry).
func PendingEntryValue(entryID string) Value[PendingEntry] {
	return MustValue[PendingEntry]("pi.pending.entry", entryID)
}

// PendingToolOutput addresses one invocation's latest progress checkpoint.
func PendingToolOutput(operationID, invocationID string) Value[harness.AgentToolResult] {
	return MustValue[harness.AgentToolResult]("pi.pending.tool_output", operationID+":"+invocationID)
}

// PendingAssistantFrames addresses one response's committed frame prefix.
func PendingAssistantFrames(operationID, responseEntryID string) ValueList[ai.AssistantMessageFrame] {
	return MustList[ai.AssistantMessageFrame]("pi.pending.assistant_frame", operationID+":"+responseEntryID)
}

// PendingToolOutputPrefix scans an operation's tool checkpoints.
func PendingToolOutputPrefix(operationID string) Value[harness.AgentToolResult] {
	return MustValue[harness.AgentToolResult]("pi.pending.tool_output", operationID+":")
}

// SessionName addresses the session name.
var SessionName = MustValue[string]("pi.session.name", "")

// EntryLabel addresses one entry label.
func EntryLabel(entryID string) Value[string] { return MustValue[string]("pi.entry.label", entryID) }

// convertStored converts an erased stored value to T: a value already of type
// T is returned unchanged (no cloning); raw JSON or another representation is
// decoded into T.
func convertStored[T any](value any) (T, error) {
	if typed, ok := value.(T); ok {
		return typed, nil
	}
	var zero T
	raw, ok := value.(json.RawMessage)
	if !ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return zero, err
		}
		raw = encoded
	}
	if frame, ok := any(&zero).(*ai.AssistantMessageFrame); ok {
		decoded, err := ai.UnmarshalAssistantMessageFrame(raw)
		if err != nil {
			return zero, err
		}
		*frame = decoded
		return zero, nil
	}
	if err := json.Unmarshal(raw, &zero); err != nil {
		return zero, fmt.Errorf("decode stored value: %w", err)
	}
	return zero, nil
}

func convertStoredValue[T any](stored StoredValue[any]) (StoredValue[T], error) {
	value, err := convertStored[T](stored.Value)
	if err != nil {
		return StoredValue[T]{}, err
	}
	return StoredValue[T]{Address: Value[T]{stored.Address.StoredAddressBase}, Value: value, Seq: stored.Seq}, nil
}

// GetValue reads one typed value from a reader; nil means absent.
func GetValue[T any](ctx context.Context, reader SessionReader, address Value[T]) (*StoredValue[T], error) {
	stored, err := reader.GetValue(ctx, address.StoredAddressBase)
	if err != nil || stored == nil {
		return nil, err
	}
	typed, err := convertStoredValue[T](*stored)
	if err != nil {
		return nil, err
	}
	return &typed, nil
}

// ScanValues reads the typed values under a namespace-scoped key prefix, in
// key-ascending order.
func ScanValues[T any](ctx context.Context, reader SessionReader, prefix Value[T]) ([]StoredValue[T], error) {
	stored, err := reader.ScanValues(ctx, prefix.StoredAddressBase)
	if err != nil {
		return nil, err
	}
	typed := make([]StoredValue[T], 0, len(stored))
	for _, value := range stored {
		converted, err := convertStoredValue[T](value)
		if err != nil {
			return nil, err
		}
		typed = append(typed, converted)
	}
	return typed, nil
}

// ReadList reads one typed page of a list.
func ReadList[T any](ctx context.Context, reader SessionReader, address ValueList[T], options *ListReadOptions) ([]ListElement[T], error) {
	elements, err := reader.ReadList(ctx, address.StoredAddressBase, options)
	if err != nil {
		return nil, err
	}
	typed := make([]ListElement[T], 0, len(elements))
	for _, element := range elements {
		value, err := convertStored[T](element.Value)
		if err != nil {
			return nil, err
		}
		typed = append(typed, ListElement[T]{Seq: element.Seq, Value: value})
	}
	return typed, nil
}
