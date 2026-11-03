package documents

// Field survival of StateDefinition: every option DefineState takes comes back from the definition, and the functions
// it was given are the ones the definition runs (Pi's defineState keeps its options object's members).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestStateDefinitionRegistrationKeepsEveryOption(t *testing.T) {
	schema := contracts.Obj(contracts.Prop("n", contracts.Int()))
	var initials, migrations int
	var checkpointArgs []int
	options := StateOptions{
		Schema:  schema,
		Version: 4,
		Initial: func() *JsonObject { initials++; return obj("n", 0.0) },
		Migrate: func(old *JsonObject, from int) (*JsonObject, error) {
			migrations++
			return obj("n", float64(from)*10), nil
		},
		Keyed:   true,
		Persist: true,
		CheckpointWhen: func(value *JsonObject, ops []delta.Op, info CheckpointInfo) bool {
			checkpointArgs = append(checkpointArgs, info.DeltasSinceBase)
			return info.DeltasSinceBase >= 3 && value != nil && len(ops) == 0
		},
	}
	registered := map[string]*StateDefinition{}
	for _, name := range []string{"survive.doc", "survive.other"} {
		registered[name] = mustDefine(t, name, options)
	}
	definition := registered["survive.doc"]
	if registered["survive.other"] == definition {
		t.Fatal("two declarations share one definition")
	}

	if definition.Name != "survive.doc" || definition.Version != 4 || !definition.Keyed || !definition.Persist {
		t.Fatalf("definition = Name %q Version %d Keyed %v Persist %v, want survive.doc 4 true true",
			definition.Name, definition.Version, definition.Keyed, definition.Persist)
	}
	if !reflect.DeepEqual(definition.Schema, schema) {
		t.Fatal("Schema is not the declared schema")
	}
	plain := mustDefine(t, "survive.plain", StateOptions{Schema: schema, Version: 1, Initial: options.Initial})
	if plain.Keyed || plain.Persist {
		t.Fatalf("an unset Keyed or Persist became true: %+v", plain)
	}
	if again := mustDefine(t, "survive.again", StateOptions{Schema: schema, Version: 9, Initial: options.Initial}); again.Fingerprint != definition.Fingerprint || definition.Fingerprint == "" {
		t.Fatalf("Fingerprint %q is not the schema's (the same schema gave %q)", definition.Fingerprint, again.Fingerprint)
	}

	if value, err := definition.Initial(background); err != nil || initials != 1 || value.Value("n") != 0.0 {
		t.Fatalf("Initial = %v, %v after %d runs of the declared function", value, err, initials)
	}
	if value, err := definition.Migrate(obj("n", 1.0), 2, background); err != nil || migrations != 1 || value.Value("n") != 20.0 {
		t.Fatalf("Migrate = %v, %v after %d runs of the declared function", value, err, migrations)
	}
	if definition.CheckpointWhen(obj("n", 1.0), nil, CheckpointInfo{DeltasSinceBase: 2}) {
		t.Fatal("CheckpointWhen stored a base for 2 deltas, the declared rule asks for 3")
	}
	if !definition.CheckpointWhen(obj("n", 1.0), nil, CheckpointInfo{DeltasSinceBase: 3}) {
		t.Fatal("CheckpointWhen did not store a base for 3 deltas")
	}
	if !reflect.DeepEqual(checkpointArgs, []int{2, 3}) {
		t.Fatalf("CheckpointWhen saw DeltasSinceBase %v, want [2 3]", checkpointArgs)
	}
}

// Without a CheckpointWhen a persisted definition never stores a base; without a Migrate an older value is refused.
func TestStateDefinitionRegistrationKeepsAbsentCheckpointWhenAndMigrateAbsent(t *testing.T) {
	definition := mustDefine(t, "survive.absent", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 2,
		Initial: func() *JsonObject { return obj("n", 0.0) },
		Persist: true,
	})
	if definition.CheckpointWhen(obj("n", 1.0), nil, CheckpointInfo{DeltasSinceBase: 1000}) {
		t.Fatal("an absent CheckpointWhen stored a base")
	}
	if _, err := definition.Migrate(obj("n", 1.0), 1, background); err == nil || !strings.Contains(err.Error(), "no migrate from version 1 to 2") {
		t.Fatalf("an absent Migrate: %v", err)
	}
}
