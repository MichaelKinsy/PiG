package durable

// Ports packages/durable/src/signals.ts
//
// Signals: typed JSON values delivered to a live task from outside, kept in its memos until a wake uses them.

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// SignalKey is the key of one wait for a signal: JSON, used in sleeps and parks. TypeScript's phantom `~value` member
// only types the key; a Go key carries none.
type SignalKey struct {
	Signal string `json:"signal"`
	Key    string `json:"key"`
}

// SignalDefinition is what DefineSignal takes.
type SignalDefinition[T any] struct {
	// Name is unique like a task or document kind; it must not contain "/".
	Name string
	// Version is the positive integer version of the value shape.
	Version int
	// Schema is a pi-schema schema of a JSON value, as the schema package builds it (a JSON Schema `bool`, `*Object` or
	// `map[string]any`); the value type is T.
	Schema schema.JsonSchema
	// Migrate turns a value stored by an older version into the current shape; nil when absent.
	Migrate func(value JsonValue, fromVersion int) (T, error)
}

// AnySignal is a signal kind with its compiled schema and migration, which only Durable reads of a token.
type AnySignal struct {
	Name    string
	Version int
	// validator is the compiled schema.
	validator *schema.Validator
	// migrate is nil when the definition has none; it returns the migrated value as JSON.
	migrate func(value JsonValue, fromVersion int) (JsonValue, error)
}

// AnySignalSource is anything that holds an erased signal definition: a SignalToken.
type AnySignalSource interface {
	AnySignal() *AnySignal
}

// SignalToken is a signal kind, built by DefineSignal: values of type T delivered under keys.
type SignalToken[T any] struct {
	Name    string
	Version int
	signal  *AnySignal
}

// AnySignal returns the erased signal definition. A zero token has none.
func (token SignalToken[T]) AnySignal() *AnySignal { return token.signal }

// Key is the key of one wait; it panics for an empty key, where upstream throws a TypeError.
func (token SignalToken[T]) Key(key string) SignalKey {
	if key == "" {
		panic(fmt.Sprintf("Signal %s key must be non-empty", token.Name))
	}
	return SignalKey{Signal: token.Name, Key: key}
}

// SignalState is a key's state: the value delivered under it, or Expired because its sleep's deadline came first.
type SignalState[T any] struct {
	Value   T
	Expired bool
}

// ErasedSignalState is a key's state with its value as JSON.
type ErasedSignalState struct {
	Value   JsonValue
	Expired bool
}

// SignalResult is what a delivery did: delivered (durably accepted, not necessarily consumed), duplicate (the key
// holds a value), invalid (not JSON, or not matching the schema), closed (the task ended, holds its outcome, is
// abort-marked, or the key expired), not_found (no such task).
type SignalResult string

// The signal results.
const (
	SignalDelivered SignalResult = "delivered"
	SignalDuplicate SignalResult = "duplicate"
	SignalInvalid   SignalResult = "invalid"
	SignalClosed    SignalResult = "closed"
	SignalNotFound  SignalResult = "not_found"
)

// AnyTaskRecord is a task record with JSON input, checkpoint, and result.
type AnyTaskRecord = TaskRecord[JsonValue, JsonValue, JsonValue]

// DefineSignal defines a signal kind. It panics for a name with "/", a version that is not a positive integer, or a
// non-JSON schema, where upstream throws a TypeError.
func DefineSignal[T any](definition SignalDefinition[T]) SignalToken[T] {
	name, version := definition.Name, definition.Version
	if name == "" || strings.Contains(name, "/") {
		panic(fmt.Sprintf("Signal name %s must be non-empty, without \"/\"", jsQuote(name)))
	}
	if version < 1 {
		panic(fmt.Sprintf("Signal %s version must be a positive integer", name))
	}
	if nonJson := nonJsonType(definition.Schema); nonJson != "" {
		panic(fmt.Sprintf("Signal %s schema admits non-JSON values (%s)", name, nonJson))
	}
	validator, err := schema.Compile(definition.Schema, schema.CompileOptions{})
	if err != nil {
		panic(fmt.Sprintf("Signal %s schema: %v", name, err))
	}
	signal := &AnySignal{Name: name, Version: version, validator: validator}
	if migrate := definition.Migrate; migrate != nil {
		signal.migrate = func(value JsonValue, fromVersion int) (JsonValue, error) {
			migrated, err := migrate(value, fromVersion)
			if err != nil {
				return nil, err
			}
			return ToJsonValue(migrated)
		}
	}
	return SignalToken[T]{Name: name, Version: version, signal: signal}
}

func jsQuote(text string) string { return fmt.Sprintf("%q", text) }

// schemaKeywords are the keywords whose value is a schema or an array of schemas. Annotations (examples, default) are
// not walked.
var schemaKeywords = []string{
	"items", "prefixItems", "additionalItems", "unevaluatedItems", "contains", "anyOf", "oneOf", "allOf", "not", "if",
	"then", "else", "additionalProperties", "unevaluatedProperties", "propertyNames",
}

// schemaMapKeywords are the keywords whose value maps names to schemas; draft 7's dependencies may also map to name
// arrays, which hold none.
var schemaMapKeywords = []string{"properties", "patternProperties", "dependentSchemas", "dependencies", "$defs", "definitions"}

// nonJsonType is a non-JSON type a schema accepts anywhere (pi-schema's bytes, bigint, TypeBox's undefined), if any.
func nonJsonType(node any) string {
	get := schemaMember(node)
	if get == nil {
		return ""
	}
	var types []any
	if value, ok := get("type"); ok {
		if list, isList := value.([]any); isList {
			types = list
		} else {
			types = []any{value}
		}
	}
	for _, kind := range types {
		if text, ok := kind.(string); ok && (text == "bytes" || text == "bigint" || text == "undefined") {
			return text
		}
	}
	var children []any
	for _, keyword := range schemaKeywords {
		value, _ := get(keyword)
		if list, ok := value.([]any); ok {
			children = append(children, list...)
		} else {
			children = append(children, value)
		}
	}
	for _, keyword := range schemaMapKeywords {
		value, _ := get(keyword)
		switch members := value.(type) {
		case *chordjson.Object:
			for _, member := range members.All() {
				children = append(children, member)
			}
		case map[string]any:
			for _, member := range members {
				children = append(children, member)
			}
		}
	}
	for _, child := range children {
		if nested := nonJsonType(child); nested != "" {
			return nested
		}
	}
	return ""
}

// schemaMember reads the members of a schema object; nil for a value that is no object (a boolean schema).
func schemaMember(node any) func(string) (any, bool) {
	switch object := node.(type) {
	case *chordjson.Object:
		if object == nil {
			return nil
		}
		return object.Get
	case map[string]any:
		return func(key string) (any, bool) { value, ok := object[key]; return value, ok }
	}
	return nil
}

// signalMemo is the stored state of one key, in the task's memo pi.signal.<name>/<key>: {version, value, consumed?} or
// {expired: true}.
type signalMemo struct {
	expired  bool
	version  int
	value    JsonValue
	consumed bool
}

// signalMemoName is the memo holding a key's state. Signal names contain no "/", so names never collide.
func signalMemoName(signal, key string) string { return "pi.signal." + signal + "/" + key }

// memoOf reads a key's memo: own entries only, nil when none. A malformed memo reads as absent.
func memoOf(memos map[string]JsonValue, name string) *signalMemo {
	raw, ok := memos[name]
	if !ok {
		return nil
	}
	object, ok := raw.(*delta.JsonObject)
	if !ok {
		return nil
	}
	if object.Has("expired") {
		return &signalMemo{expired: true}
	}
	version, _ := object.Value("version").(float64)
	value, _ := object.Get("value")
	consumed, _ := object.Value("consumed").(bool)
	return &signalMemo{version: int(version), value: value, consumed: consumed}
}

func (memo signalMemo) json() JsonValue {
	if memo.expired {
		return delta.JsonObjectOf("expired", true)
	}
	object := delta.JsonObjectOf("version", float64(memo.version), "value", memo.value)
	if memo.consumed {
		object.Set("consumed", true)
	}
	return object
}

// withMemos is record with memos added; signal memos are written only to live records.
func withMemos(record AnyTaskRecord, memos map[string]JsonValue) AnyTaskRecord {
	merged := make(map[string]JsonValue, len(record.Memos)+len(memos))
	for name, value := range record.Memos {
		merged[name] = value
	}
	for name, value := range memos {
		merged[name] = value
	}
	record.Memos = merged
	return record
}

// DeliveredSignal is the decision of a delivery: the result, and the record to write when it changes.
type DeliveredSignal struct {
	Result SignalResult
	// Record is nil when the delivery changes nothing.
	Record *AnyTaskRecord
}

// DeliverSignal decides a delivery to record (spec suspendable §3.2): the result, and the record to write when it
// changes, with the value stored, or the key marked expired when the task sleeps on it past its deadline. A nil record
// is a task that does not exist. value is the delivered value as JSON; a value that is no JSON must be answered invalid
// by the caller.
func DeliverSignal(record *AnyTaskRecord, token AnySignalSource, key string, value JsonValue, now float64) DeliveredSignal {
	if record == nil {
		return DeliveredSignal{Result: SignalNotFound}
	}
	state := record.State
	if state.Status == TaskTerminal || state.Status == TaskCompleting || record.AbortRequested {
		return DeliveredSignal{Result: SignalClosed}
	}
	signal := token.AnySignal()
	// Values come from outside: the trust boundary of the signal's schema.
	if key == "" || !signal.accepts(value) {
		return DeliveredSignal{Result: SignalInvalid}
	}
	name := signalMemoName(signal.Name, key)
	if stored := memoOf(record.Memos, name); stored != nil {
		if stored.expired {
			return DeliveredSignal{Result: SignalClosed}
		}
		return DeliveredSignal{Result: SignalDuplicate}
	}
	if state.Status == TaskSleeping && state.Until != nil && *state.Until <= now && listsSignal(state.Signals, signal.Name, key) {
		expired := withMemos(*record, map[string]JsonValue{name: signalMemo{expired: true}.json()})
		return DeliveredSignal{Result: SignalClosed, Record: &expired}
	}
	stored := withMemos(*record, map[string]JsonValue{name: signalMemo{version: signal.Version, value: value}.json()})
	return DeliveredSignal{Result: SignalDelivered, Record: &stored}
}

// accepts reports whether value is a JSON value the signal's schema admits.
func (signal *AnySignal) accepts(value JsonValue) bool {
	if signal == nil || signal.validator == nil {
		return false
	}
	if _, err := ToJsonValue(value); err != nil {
		return false
	}
	ok, err := signal.validator.Check(value)
	return err == nil && ok
}

func listsSignal(signals []SignalKey, signal, key string) bool {
	for _, listed := range signals {
		if listed.Signal == signal && listed.Key == key {
			return true
		}
	}
	return false
}

// ReadSignalMemo is a key's state in memos, the value migrated to the token's version; nil while nothing came. It
// fails for a stored version newer than the token's, a missing or failing migrate, or a migrated value the schema
// rejects.
func ReadSignalMemo(memos map[string]JsonValue, token AnySignalSource, key string) (*ErasedSignalState, error) {
	signal := token.AnySignal()
	stored := memoOf(memos, signalMemoName(signal.Name, key))
	if stored == nil {
		return nil, nil
	}
	if stored.expired {
		return &ErasedSignalState{Expired: true}, nil
	}
	if stored.version == signal.Version {
		return &ErasedSignalState{Value: stored.value}, nil
	}
	if stored.version > signal.Version {
		return nil, fmt.Errorf("Signal %s value has version %d, newer than %d", signal.Name, stored.version, signal.Version)
	}
	if signal.migrate == nil {
		return nil, fmt.Errorf("Signal %s has no migration from version %d", signal.Name, stored.version)
	}
	migrated, err := signal.migrate(stored.value, stored.version)
	if err != nil {
		return nil, err
	}
	if !signal.accepts(migrated) {
		return nil, fmt.Errorf("Signal %s migration from version %d returned an invalid value", signal.Name, stored.version)
	}
	return &ErasedSignalState{Value: migrated}, nil
}

// SignalReader reads a signal key's committed state in the task it belongs to: a task runtime, a hook's api, a tool's
// api. A Go method cannot take the token's type parameter, so ReadSignal is the typed read.
type SignalReader interface {
	ReadSignalErased(ctx context.Context, token AnySignalSource, key string) (*ErasedSignalState, error)
}

// ReadSignal is `readSignal(token, key, context)` of TaskRuntime, HookApi, and ToolExecutionApi: a signal key's
// committed state, the value migrated to the token's version; nil while nothing came. It fails when the stored value
// cannot be migrated or the migrated value does not match the schema.
func ReadSignal[T any](ctx context.Context, reader SignalReader, token SignalToken[T], key string) (*SignalState[T], error) {
	state, err := reader.ReadSignalErased(ctx, token, key)
	if err != nil || state == nil {
		return nil, err
	}
	if state.Expired {
		return &SignalState[T]{Expired: true}, nil
	}
	value, err := FromJsonValue[T](state.Value)
	if err != nil {
		return nil, err
	}
	return &SignalState[T]{Value: value}, nil
}

// SignalTx is the part of Tx that delivers signals: Tx itself, whose SignalErased a Go method cannot type with T.
type SignalTx interface {
	SignalErased(taskId TaskId, token AnySignalSource, key string, value JsonValue) (SignalResult, error)
}

// Signal is `tx.signal(taskId, token, key, value)`: deliver value to task taskId under key, stored in the task until a
// sleep on the key wakes on it, also when the task does not sleep on it yet. It reads the task, so it fails with
// ReadAfterWrite after the commit's first table write; the result is provisional until the commit is stored. It checks
// nothing about the caller. A value that is no JSON is answered invalid.
func Signal[T any](tx SignalTx, taskId TaskId, token SignalToken[T], key string, value T) (SignalResult, error) {
	encoded, err := ToJsonValue(value)
	if err != nil {
		return SignalInvalid, nil
	}
	return tx.SignalErased(taskId, token, key, encoded)
}

// CheckSleep fails unless a sleep waits for something (until or a non-empty signals) and none of its keys served a
// wait.
func CheckSleep(state TaskState[JsonValue, JsonValue], memos map[string]JsonValue, task TaskId) error {
	if state.Until == nil && len(state.Signals) == 0 {
		return fmt.Errorf("Task %d cannot sleep without until or signals", task)
	}
	for _, listed := range state.Signals {
		stored := memoOf(memos, signalMemoName(listed.Signal, listed.Key))
		if stored == nil {
			continue
		}
		if stored.expired {
			return fmt.Errorf("Task %d cannot sleep on signal %s key %s: it expired", task, listed.Signal, listed.Key)
		}
		if stored.consumed {
			return fmt.Errorf("Task %d cannot sleep on signal %s key %s: it already woke a wait", task, listed.Signal, listed.Key)
		}
	}
	return nil
}

// SleepDue reports whether a sleep can wake now: a listed key holds a value no wake used, or its deadline came.
func SleepDue(state TaskState[JsonValue, JsonValue], memos map[string]JsonValue, now float64) bool {
	return readyKey(state, memos) != nil || (state.Until != nil && *state.Until <= now)
}

// SleepWoke is the wake of a sleep that can wake now, and the memos the waking reservation writes.
type SleepWoke struct {
	Wake  WakeReason
	Memos map[string]JsonValue
}

// SleepWake is the wake of a sleep that can wake now, and the memos the waking reservation writes: the first listed
// key holding a value no wake used, marked consumed; otherwise the deadline, with every listed key that holds nothing
// marked expired. Nil when the sleep cannot wake yet.
func SleepWake(state TaskState[JsonValue, JsonValue], memos map[string]JsonValue, now float64) *SleepWoke {
	if ready := readyKey(state, memos); ready != nil {
		name := signalMemoName(ready.Signal, ready.Key)
		consumed := *memoOf(memos, name)
		consumed.consumed = true
		return &SleepWoke{
			Wake:  WakeReason{Reason: WakeSignal, Signal: ready.Signal, Key: ready.Key},
			Memos: map[string]JsonValue{name: consumed.json()},
		}
	}
	if state.Until == nil || *state.Until > now {
		return nil
	}
	if len(state.Signals) == 0 {
		return &SleepWoke{Wake: WakeReason{Reason: WakeTime}, Memos: map[string]JsonValue{}}
	}
	expired := map[string]JsonValue{}
	for _, listed := range state.Signals {
		name := signalMemoName(listed.Signal, listed.Key)
		if memoOf(memos, name) == nil {
			expired[name] = signalMemo{expired: true}.json()
		}
	}
	return &SleepWoke{Wake: WakeReason{Reason: WakeExpired}, Memos: expired}
}

// readyKey is the first listed key, in signals order, holding a value no wake used.
func readyKey(state TaskState[JsonValue, JsonValue], memos map[string]JsonValue) *SignalKey {
	for index := range state.Signals {
		stored := memoOf(memos, signalMemoName(state.Signals[index].Signal, state.Signals[index].Key))
		if stored != nil && !stored.expired && !stored.consumed {
			return &state.Signals[index]
		}
	}
	return nil
}

// IsSettled reports whether a submission settled: answered (done) or not (unanswered).
func IsSettled(record SubmissionRecord) bool {
	return record.Status == SubmissionDone || record.Status == SubmissionUnanswered
}

// settledSubmission is one variant of the schema of SubmissionSettled: a settled record of type and status with the
// fields besides the common ones; other fields are allowed.
func settledSubmission(kind, status string, fields ...schema.Property) schema.Schema {
	properties := schema.Properties{
		schema.Prop("id", schema.Int()),
		schema.Prop("conversationId", schema.Int()),
		schema.Opt("requestId", schema.Str()),
		schema.Opt("notify", schema.Obj(schema.Properties{schema.Prop("taskId", schema.Int()), schema.Prop("key", schema.Str())})),
		schema.Prop("type", schema.Lit(kind)),
		schema.Prop("status", schema.Lit(status)),
	}
	return schema.Obj(append(properties, fields...), schema.Open())
}

// SubmissionSettledSignal is the built-in SubmissionSettled signal (upstream's name; Go's SubmissionSettled is the
// settled result of Submission.Abort): delivered to SubmissionDraft.Notify when the submission settles, in the settling
// commit. Its value is the settled record, what Submission.Wait resolves to; its schema checks each variant's required
// fields, so another delivery cannot take the key with something else.
var SubmissionSettledSignal = DefineSignal[SettledSubmissionRecord](SignalDefinition[SettledSubmissionRecord]{
	Name:    "pi.submission-settled",
	Version: 1,
	Schema: schema.Unsafe(schema.Union([]schema.Schema{
		settledSubmission("input", "done", schema.Prop("entry", schema.Int()), schema.Prop("answer", schema.Int())),
		settledSubmission("write", "done", schema.Prop("entry", schema.Int())),
		settledSubmission("continue", "done", schema.Prop("answer", schema.Int())),
		settledSubmission("input", "unanswered", schema.Opt("entry", schema.Int()), schema.Prop("reason", schema.Str())),
		settledSubmission("write", "unanswered", schema.Prop("reason", schema.Str())),
		settledSubmission("continue", "unanswered", schema.Prop("reason", schema.Str())),
	})),
})
