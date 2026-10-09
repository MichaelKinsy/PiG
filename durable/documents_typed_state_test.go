package durable

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

type typedStateDoc struct {
	A string `json:"a"`
}

// manualSource is an authoritative source the test drives frame by frame.
type manualSource struct {
	snapshot JsonObject
	cursor   int
	listener func(chord.ReplicatedStateSourceFrame[JsonObject])
}

func (source *manualSource) Attach() (chord.ReplicatedStateSourceAttachment[JsonObject], error) {
	return source, nil
}

func (source *manualSource) Snapshot() chord.ReplicatedStateSnapshot[JsonObject] {
	return chord.ReplicatedStateSnapshot[JsonObject]{Value: source.snapshot, Cursor: source.cursor}
}

func (source *manualSource) Activate(listener func(chord.ReplicatedStateSourceFrame[JsonObject])) error {
	source.listener = listener
	return nil
}

func (source *manualSource) Dispose() {}

func (source *manualSource) emit(cursor int, value JsonObject) {
	source.listener(chord.ReplicatedStateSourceFrame[JsonObject]{Cursor: cursor, Value: value, Ops: []delta.Op{}, Context: context.Background()})
}

func attachTyped(t *testing.T, source *manualSource, onError func(error), publishedBefore ...JsonObject) (*typedStateSource[typedStateDoc], *chord.AttachedReplicatedState[*typedStateDoc]) {
	t.Helper()
	erased, err := chord.AttachReplicatedStateSource[JsonObject](source, chord.ReplicatedStateSourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range publishedBefore {
		source.cursor++
		source.emit(source.cursor, value)
	}
	typedSource := &typedStateSource[typedStateDoc]{state: erased, onError: onError}
	typed, err := chord.AttachReplicatedStateSource[*typedStateDoc](typedSource, chord.ReplicatedStateSourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return typedSource, typed
}

// A publication whose sequence is not after the attachment's snapshot is part of the snapshot, so it is not delivered
// again as a frame.
func TestTypedStateSourceIgnoresPublicationsAtOrBeforeTheSnapshotCursor(t *testing.T) {
	source := &manualSource{snapshot: delta.JsonObjectOf("a", "w")}
	// Two publications precede the attachment, so its snapshot cursor is 2.
	typedSource, typed := attachTyped(t, source, nil, delta.JsonObjectOf("a", "v"), delta.JsonObjectOf("a", "x"))
	var values []string
	if _, err := typed.Subscribe(func(value *typedStateDoc, _ context.Context, _ chord.ReplicatedStateDelivery) {
		values = append(values, value.A)
	}); err != nil {
		t.Fatal(err)
	}
	typedSource.receive(nil, 2, context.Background())
	typedSource.receive(nil, 1, context.Background())
	// The one delivery is the snapshot itself.
	if len(values) != 1 || values[0] != "x" {
		t.Fatalf("publications at or before the snapshot cursor were delivered: %v", values)
	}
	source.emit(3, delta.JsonObjectOf("a", "y"))
	if got := typed.Value(); got == nil || got.A != "y" {
		t.Fatalf("value after the first later frame: %+v", got)
	}
}

// A revision that does not decode as the token's type is a source-contract failure of the attached state: delivery
// stops and the failure reaches OnError, where it used to stop without a word.
func TestTypedStateSourceReportsARevisionThatDoesNotDecode(t *testing.T) {
	source := &manualSource{snapshot: delta.JsonObjectOf("a", "x")}
	var failures []error
	_, typed := attachTyped(t, source, func(err error) { failures = append(failures, err) })
	var values []string
	if _, err := typed.Subscribe(func(value *typedStateDoc, _ context.Context, _ chord.ReplicatedStateDelivery) {
		values = append(values, value.A)
	}); err != nil {
		t.Fatal(err)
	}
	source.emit(1, delta.JsonObjectOf("a", float64(5)))
	source.emit(2, delta.JsonObjectOf("a", "z"))
	if len(failures) != 1 {
		t.Fatalf("failures %v, want one", failures)
	}
	if len(values) != 1 || values[0] != "x" {
		t.Fatalf("delivery went on after a revision that does not decode: %v", values)
	}
}

// types.ts:84-88: a family definition written in Pi's shape carries `family: true` (documents.ts:31-34 requires it at compile time, so DefineDocFamily panics
// without it); the erased definition is a family (read at documents.ts:123 and transaction.ts:525, 609); a singleton definition is not.
func TestDocFamilyDefinitionCarriesPisFamilyDiscriminator(t *testing.T) {
	semantics := DocumentSemantics{Scope: ScopeSession}
	common := CommonDocDefinition[JsonObject]{Kind: "family.test", Version: 1}
	initial := func(JsonValue) JsonObject { return delta.NewJsonObject(0) }
	family := DefineDocFamily(DocFamilyDefinition[JsonObject, JsonValue]{Kind: common.Kind, Version: common.Version, DocumentSemantics: semantics, Family: true, Initial: initial})
	if !family.AnyDefinition().Family {
		t.Error("the erased definition is not a family")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("a family definition without family: true was accepted")
			}
		}()
		DefineDocFamily(DocFamilyDefinition[JsonObject, JsonValue]{Kind: common.Kind, Version: common.Version, DocumentSemantics: semantics, Initial: initial})
	}()
	common.Initial = func() JsonObject { return delta.NewJsonObject(0) }
	single := DefineDoc(DocDefinition[JsonObject]{CommonDocDefinition: common, DocumentSemantics: semantics})
	if single.AnyDefinition().Family {
		t.Error("a singleton definition is a family")
	}
}
