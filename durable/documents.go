package durable

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// Ports packages/durable/src/documents.ts

// AnyDocDefinition is the erased definition shape the Session uses after token resolution. Values are JSON objects.
type AnyDocDefinition struct {
	DocumentSemantics
	Kind    string
	Version int
	Family  bool
	// Initial returns the initial value; seed is nil for singletons.
	Initial func(seed JsonValue) (JsonObject, error)
	// Migrate is nil when the definition has no migration.
	Migrate func(value JsonObject, fromVersion int) (JsonObject, error)
	// CheckpointWhen decides whether an ordinary change is stored as a complete base. Every definition has one: DefineDoc
	// and DefineDocFamily reject a definition with neither CheckpointWhen nor CheckpointWhenRaw, as upstream's types require
	// checkpointWhen on every definition (documents.ts AnyDocDefinition, types.ts CommonDocDefinition).
	CheckpointWhen func(value JsonObject, ops []Op, info CheckpointInfo) (bool, error)
}

// AnyDocToken is an erased singleton or family token.
type AnyDocToken interface {
	AnyDefinition() *AnyDocDefinition
}

// DocTokenOf is a singleton or family token of value type T.
type DocTokenOf[T any] interface {
	AnyDocToken
	docType(T)
}

// AnyDefinition returns the erased definition.
func (token DocToken[T]) AnyDefinition() *AnyDocDefinition { return token.definition }

// AnyDefinition returns the erased definition.
func (token DocFamilyToken[T, I]) AnyDefinition() *AnyDocDefinition { return token.definition }

func (DocToken[T]) docType(T)          {}
func (DocFamilyToken[T, I]) docType(T) {}

// DefineDoc defines a singleton document. Scope selects a Session, conversation, or task document; conversation
// documents also declare History and Fork. It panics when the version is not a positive integer, where upstream
// throws a TypeError at definition time, when DocumentSemantics is not a member of upstream's union (types.ts:42-60),
// which upstream's overloads reject at compile time, and when the definition has neither CheckpointWhen nor
// CheckpointWhenRaw: upstream's types require checkpointWhen with no default (types.ts:77-78), so the Go definition names
// its policy too, `func(T, []Op, CheckpointInfo) bool { return false }` to keep every change a delta.
func DefineDoc[T any](definition DocDefinition[T]) DocToken[T] {
	initial := definition.Initial
	erased := eraseDocDefinition(definition.CommonDocDefinition, definition.DocumentSemantics, false, func(_ JsonValue) (JsonObject, error) {
		return ToJsonObject(initial())
	})
	validateDefinition(erased)
	return DocToken[T]{definition: erased}
}

// DefineDocFamily defines a keyed document family; Initial(seed) runs only when a member is absent. It panics when
// the version is not a positive integer, DocumentSemantics is not a member of upstream's union (types.ts:42-60), CheckpointWhen is absent (see [DefineDoc]), or Family is not true: upstream's FamilyInput requires `family: true` at compile time (documents.ts:31-34),
// and the erased definition's family flag is what addressing reads (resolveAddress).
func DefineDocFamily[T, I any](definition DocFamilyDefinition[T, I]) DocFamilyToken[T, I] {
	if !definition.Family {
		panic(fmt.Sprintf("Document %s family must be true", definition.Kind))
	}
	initial := definition.Initial
	erased := eraseDocDefinition(CommonDocDefinition[T]{Kind: definition.Kind, Version: definition.Version, Migrate: definition.Migrate, CheckpointWhen: definition.CheckpointWhen}, definition.DocumentSemantics, definition.Family, func(seed JsonValue) (JsonObject, error) {
		typedSeed, err := FromJsonValue[I](seed)
		if err != nil {
			return nil, err
		}
		return ToJsonObject(initial(typedSeed))
	})
	validateDefinition(erased)
	return DocFamilyToken[T, I]{definition: erased}
}

func eraseDocDefinition[T any](common CommonDocDefinition[T], semantics DocumentSemantics, family bool, initial func(JsonValue) (JsonObject, error)) *AnyDocDefinition {
	erased := &AnyDocDefinition{
		DocumentSemantics: semantics,
		Kind:              common.Kind,
		Version:           common.Version,
		Family:            family,
		Initial:           initial,
	}
	if migrate := common.Migrate; migrate != nil {
		erased.Migrate = func(value JsonObject, fromVersion int) (JsonObject, error) {
			migrated, err := migrate(value, fromVersion)
			if err != nil {
				return nil, err
			}
			return ToJsonObject(migrated)
		}
	}
	if raw := common.CheckpointWhenRaw; raw != nil {
		erased.CheckpointWhen = func(value JsonObject, ops []Op, info CheckpointInfo) (bool, error) {
			if err := checkDecodes[T](value, ops); err != nil {
				return false, err
			}
			return raw(value, ops, info), nil
		}
	} else if checkpointWhen := common.CheckpointWhen; checkpointWhen != nil {
		erased.CheckpointWhen = func(value JsonObject, ops []Op, info CheckpointInfo) (bool, error) {
			// A JSON-object document receives the exact prepared revision, as upstream passes it.
			if exact, ok := any(value).(T); ok {
				return checkpointWhen(exact, ops, info), nil
			}
			typed, err := FromJsonValue[T](value)
			if err != nil {
				return false, err
			}
			return checkpointWhen(typed, ops, info), nil
		}
	}
	return erased
}

func validateDefinition(definition *AnyDocDefinition) {
	if definition.Version < 1 || definition.Version > maxSafeInteger {
		panic(fmt.Sprintf("Document %s version must be a positive integer", definition.Kind))
	}
	if err := validateDocumentSemantics(definition.DocumentSemantics); err != nil {
		panic(fmt.Sprintf("Document %s %v", definition.Kind, err))
	}
	if definition.CheckpointWhen == nil {
		panic(fmt.Sprintf("Document %s checkpointWhen is required", definition.Kind))
	}
}

// validateDocumentSemantics admits the combinations of Pi's DocumentSemantics union (types.ts:42-60), which the compiler enforces upstream: a
// session or task document declares neither history nor fork, a latest conversation document forks "current" or "initial", and a rewindable
// one forks "asOf", "current" or "initial".
func validateDocumentSemantics(semantics DocumentSemantics) error {
	switch semantics.Scope {
	case ScopeSession, ScopeTask:
		if semantics.History != "" || semantics.Fork != "" {
			return fmt.Errorf("semantics: only conversation documents declare history and fork, not a %s document", semantics.Scope)
		}
	case ScopeConversation:
		switch semantics.History {
		case HistoryLatest:
			if semantics.Fork != ForkCurrent && semantics.Fork != ForkInitial {
				return fmt.Errorf("semantics: a latest conversation document forks \"current\" or \"initial\", not %q", semantics.Fork)
			}
		case HistoryRewindable:
			if semantics.Fork != ForkAsOf && semantics.Fork != ForkCurrent && semantics.Fork != ForkInitial {
				return fmt.Errorf("semantics: a rewindable conversation document forks \"asOf\", \"current\" or \"initial\", not %q", semantics.Fork)
			}
		default:
			return fmt.Errorf("semantics: a conversation document's history is \"latest\" or \"rewindable\", not %q", semantics.History)
		}
	default:
		return fmt.Errorf("semantics: scope is \"session\", \"conversation\" or \"task\", not %q", semantics.Scope)
	}
	return nil
}

const maxSafeInteger = 1<<53 - 1

// ResolvedAddress is a logical address plus its string identity for maps.
type ResolvedAddress struct {
	Address DocumentAddress
	Id      string
	// NextArgument is the index after the owner and family key.
	NextArgument int
}

// ResolveAddress resolves an overloaded argument list and returns the index after the owner and family key.
func ResolveAddress(definition *AnyDocDefinition, args []any) (ResolvedAddress, error) {
	index := 0
	var scope DocumentRecordScope
	switch definition.Scope {
	case ScopeSession:
		scope = DocumentRecordScope{Kind: ScopeSession}
	case ScopeConversation:
		id, err := ownerId(argumentAt(args, index), definition)
		if err != nil {
			return ResolvedAddress{}, err
		}
		index++
		scope = DocumentRecordScope{Kind: ScopeConversation, ConversationId: ConversationId(id)}
	case ScopeTask:
		id, err := ownerId(argumentAt(args, index), definition)
		if err != nil {
			return ResolvedAddress{}, err
		}
		index++
		scope = DocumentRecordScope{Kind: ScopeTask, TaskId: TaskId(id)}
	default:
		return ResolvedAddress{}, fmt.Errorf("Document %s has unknown scope %q", definition.Kind, definition.Scope)
	}
	address := DocumentAddress{Kind: definition.Kind, Scope: scope}
	if definition.Family {
		// Upstream casts the argument to a string without checking it; an absent key leaves the address keyless.
		if key, ok := argumentAt(args, index).(string); ok {
			address.Key = &key
		}
		index++
	}
	return ResolvedAddress{Address: address, Id: AddressId(address), NextArgument: index}, nil
}

func argumentAt(args []any, index int) any {
	if index < len(args) {
		return args[index]
	}
	return nil
}

func ownerId(value any, definition *AnyDocDefinition) (int64, error) {
	id, ok := int64(0), false
	switch typed := value.(type) {
	case ConversationId:
		id, ok = int64(typed), true
	case TaskId:
		id, ok = int64(typed), true
	case EntryId:
		id, ok = int64(typed), true
	case SubmissionId:
		id, ok = int64(typed), true
	case DocumentId:
		id, ok = int64(typed), true
	case int:
		id, ok = int64(typed), true
	case int64:
		id, ok = typed, true
	case float64:
		if typed == math.Trunc(typed) && math.Abs(typed) <= maxSafeInteger {
			id, ok = int64(typed), true
		}
	}
	// Number.isSafeInteger bounds every numeric owner, whatever its Go type.
	if ok && id >= -maxSafeInteger && id <= maxSafeInteger {
		return id, nil
	}
	return 0, fmt.Errorf("Document %s requires a %s ID", definition.Kind, definition.Scope)
}

// AddressId returns the stable string identity of one logical address: the JSON text of
// [kind, scope kind, owner or null, key or null].
func AddressId(address DocumentAddress) string {
	var owner any
	switch address.Scope.Kind {
	case ScopeConversation:
		owner = int64(address.Scope.ConversationId)
	case ScopeTask:
		owner = int64(address.Scope.TaskId)
	}
	var key any
	if address.Key != nil {
		key = *address.Key
	}
	encoded, err := json.Marshal([]any{address.Kind, address.Scope.Kind, owner, key})
	if err != nil {
		panic(err)
	}
	canonical, err := jsonstringify.Canonicalize(encoded)
	if err != nil {
		panic(err)
	}
	return string(canonical)
}

// BuildDocumentCreate builds the storage create record for a new incarnation at an address (upstream
// documentCreate).
func BuildDocumentCreate(definition *AnyDocDefinition, address DocumentAddress, id DocumentId) DocumentCreate {
	create := DocumentCreate{Id: id, Kind: address.Kind, Key: address.Key, Scope: address.Scope}
	if address.Scope.Kind == ScopeConversation {
		create.History = definition.History
		create.Fork = definition.Fork
	}
	return create
}

// CheckRecordScope rejects typed access whose token disagrees with the persisted scope, history, or fork semantics.
func CheckRecordScope(definition *AnyDocDefinition, record DocumentCreate) error {
	if record.Scope.Kind != definition.Scope || (record.Scope.Kind == ScopeConversation && (record.History != definition.History || record.Fork != definition.Fork)) {
		return fmt.Errorf("Document %d (%s) does not match the supplied definition semantics", record.Id, record.Kind)
	}
	return nil
}

// CheckRecordVersion rejects typed access to a stored version the supplied definition cannot use.
func CheckRecordVersion(definition *AnyDocDefinition, record DocumentCreate, version int) error {
	if version > definition.Version {
		return fmt.Errorf("Document %d (%s) has newer version %d than %d", record.Id, record.Kind, version, definition.Version)
	}
	if version < definition.Version && definition.Migrate == nil {
		return fmt.Errorf("Document %d (%s) requires migration from version %d", record.Id, record.Kind, version)
	}
	return nil
}

// MaterializeDocument validates and materializes a detached stored value for typed access.
func MaterializeDocument(definition *AnyDocDefinition, stored StoredDocument) (JsonObject, error) {
	return MaterializeDocumentValue(definition, stored.Record.AsCreate(), stored.Version, stored.Value)
}

// MaterializeDocumentValue validates and materializes one detached value before its first persisted incarnation.
func MaterializeDocumentValue(definition *AnyDocDefinition, record DocumentCreate, version int, value JsonObject) (JsonObject, error) {
	if err := CheckRecordScope(definition, record); err != nil {
		return nil, err
	}
	if err := CheckRecordVersion(definition, record, version); err != nil {
		return nil, err
	}
	if version == definition.Version {
		return value, nil
	}
	migrated, err := definition.Migrate(value, version)
	if err != nil {
		return nil, err
	}
	return ToJsonObject(migrated)
}

// AsCreate returns the create fields of a record, without the storage stamps.
func (record DocumentRecord) AsCreate() DocumentCreate {
	return DocumentCreate{Id: record.Id, Kind: record.Kind, Key: record.Key, Scope: record.Scope, History: record.History, Fork: record.Fork}
}

// TxDoc returns the draft of a document in a commit, creating it when absent. args are the owner ID (conversation or
// task scope), then the family key and seed (families).
func TxDoc[T any](tx Tx, token DocTokenOf[T], args ...any) (Draft[T], error) {
	return tx.Doc(token, args...)
}

// DecodeDoc decodes a committed JSON document value as T; nil stays nil.
func DecodeDoc[T any](value JsonObject) (*T, error) {
	if value == nil {
		return nil, nil
	}
	typed, err := FromJsonValue[T](value)
	if err != nil {
		return nil, err
	}
	return &typed, nil
}

// Snapshot returns the committed value of a document, or nil when absent. args are the owner ID and family key as
// for TxDoc.
func Snapshot[T any](ctx context.Context, reader DocumentReader, token DocTokenOf[T], args ...any) (*T, error) {
	value, err := reader.SnapshotErased(ctx, token, args...)
	if err != nil {
		return nil, err
	}
	return DecodeDoc[T](value)
}

// SnapshotAsOf returns the committed value of a rewindable conversation document as of the visible entry at, or nil
// when absent. args are the conversation ID, then the family key for families.
func SnapshotAsOf[T any](ctx context.Context, reader DocumentReader, token DocTokenOf[T], at EntryId, args ...any) (*T, error) {
	value, err := reader.SnapshotAsOfErased(ctx, token, at, args...)
	if err != nil {
		return nil, err
	}
	return DecodeDoc[T](value)
}

// typedWatch is a document watch whose values decode as T.
type typedWatch[T any] struct {
	WatchHandle[JsonObject]
	// decoded is the latest value that decoded as T; Value never panics, as upstream's getter cannot fail.
	decoded *atomic.Pointer[typedValue[T]]
}

type typedValue[T any] struct{ value *T }

// Value is the acquisition or latest delivered revision; nil once the document retired. A revision that does not
// decode as the token's type breaks the document's definition: Start's listener fails and ends the watch with its
// listener_error, and Value keeps returning the latest revision that decoded.
func (watch typedWatch[T]) Value() *T {
	if value, err := DecodeDoc[T](watch.WatchHandle.Value()); err == nil {
		watch.decoded.Store(&typedValue[T]{value})
		return value
	}
	return watch.decoded.Load().value
}

// Start installs the sole listener; a value that does not decode ends the watch with its listener_error.
func (watch typedWatch[T]) Start(listener func(ctx context.Context, value *T, ops []Op) error) {
	watch.WatchHandle.Start(func(ctx context.Context, value JsonObject, ops []Op) error {
		decoded, err := DecodeDoc[T](value)
		if err != nil {
			return err
		}
		watch.decoded.Store(&typedValue[T]{decoded})
		return listener(ctx, decoded, ops)
	})
}

// WatchDoc returns a watch of a document's committed value decoded as T, or nil when the document is absent; the
// value is nil once the document retires. args are the owner ID and family key as for TxDoc. The erased form is
// DocumentObserver.WatchDocErased.
func WatchDoc[T any](ctx context.Context, observer DocumentObserver, token DocTokenOf[T], args ...any) (DocumentWatch[T], error) {
	watch, err := observer.WatchDocErased(ctx, token, args...)
	if err != nil || watch == nil {
		return nil, err
	}
	// The acquisition value is checked like Snapshot's.
	acquired, err := DecodeDoc[T](watch.Value())
	if err != nil {
		_, _ = watch.Stop()
		return nil, err
	}
	decoded := new(atomic.Pointer[typedValue[T]])
	decoded.Store(&typedValue[T]{acquired})
	return typedWatch[T]{watch, decoded}, nil
}

// TxRetireDoc retires a document; args are the owner ID and family key as for TxDoc, without a seed.
func TxRetireDoc[T any](tx Tx, token DocTokenOf[T], args ...any) error {
	return tx.RetireDoc(token, args...)
}
