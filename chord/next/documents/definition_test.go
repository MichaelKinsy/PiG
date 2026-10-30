package documents

// Ports packages/chord/test/next/documents/definition.test.ts

import (
	"math"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

func mustDefine(t *testing.T, name string, options StateOptions) *StateDefinition {
	t.Helper()
	definition, err := DefineState(name, options)
	if err != nil {
		t.Fatalf("DefineState(%q): %v", name, err)
	}
	return definition
}

// Designed out: expectTypeOf(Notes).toEqualTypeOf<StateDefinition<{ list: string[] }, false>> (the value type and
// the keyedness as type parameters). The Go mechanism: every document value is *JsonObject (DocumentOf<S>), the
// definition is the one *StateDefinition type, and keyedness is the runtime Keyed field asserted below.
var _ *JsonObject = (*delta.JsonObject)(nil)

func TestDefineStateTypesTheValueAndWhetherItIsKeyed(t *testing.T) {
	notes := mustDefine(t, "notes", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("list", contracts.Arr(contracts.Str()))),
		Version: 1,
		Initial: func() *JsonObject { return obj("list", []any{}) },
	})
	drafts := mustDefine(t, "drafts", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *JsonObject { return obj("n", 0.0) },
		Keyed:   true,
	})
	if got := []bool{notes.Keyed, drafts.Keyed}; !reflect.DeepEqual(got, []bool{false, true}) {
		t.Fatalf("keyed = %v", got)
	}
}

func TestDefineStateTakesARestrictedNameOnly(t *testing.T) {
	options := StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *JsonObject { return obj("n", 0.0) },
	}
	for _, name := range []string{"@app/notes.v_1~x-y", "n"} {
		if got := mustDefine(t, name, options).Name; got != name {
			t.Errorf("name = %q, want %q", got, name)
		}
	}
	// name and JSON.stringify(name); "a\uD800" is the lone surrogate as the WTF-8 bytes of U+D800.
	for _, c := range []struct{ name, quoted string }{
		{"", `""`},
		{"Notes", `"Notes"`},
		{"app:notes", `"app:notes"`},
		{"app notes", `"app notes"`},
		{"a\x00b", `"a\u0000b"`},
		{"a\xed\xa0\x80", `"a\ud800"`},
		{"é", `"é"`},
	} {
		_, err := DefineState(c.name, options)
		want := "A state name " + c.quoted + ": expected lowercase [a-z0-9@/._~-]"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("DefineState(%q) = %v, want an error containing %q", c.name, err, want)
		}
	}
	// "Plugin code, not type-checked": defineState(42 as never, options) throws "A state name 42". A Go name is a
	// string, so a non-string name is designed out (the compiler refuses it).
}

func TestInitialIsCheckedAgainstTheSchemaOnceTheFirstTimeItIsUsed(t *testing.T) {
	meter := nexttest.Metered(t)
	calls := 0
	doc := mustDefine(t, "doc", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *JsonObject { calls++; return obj("n", 0.0) },
	})
	for range 2 {
		value, err := doc.Initial(meter.Context)
		if err != nil {
			t.Fatal(err)
		}
		if got := nexttest.JSONText(t, value); got != `{"n":0}` {
			t.Fatalf("initial = %s", got)
		}
	}
	if calls != 2 {
		t.Errorf("initial called %d times, want 2", calls)
	}
	want := map[string]float64{"chord.data.compilations": 1, "chord.data.validations": 1}
	if got := meter.Counts(); !reflect.DeepEqual(got, want) {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

func TestInitialRefusesAValueThatDoesNotMatchEveryTimeUntilOneDoes(t *testing.T) {
	value := obj("n", "x")
	doc := mustDefine(t, "doc", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *JsonObject { return value },
	})
	for range 2 {
		if _, err := doc.Initial(background); err == nil || !strings.Contains(err.Error(), "doc: initial() does not match the schema") {
			t.Fatalf("err = %v", err)
		}
	}
	value = obj("n", 1.0)
	got, err := doc.Initial(background)
	if err != nil {
		t.Fatal(err)
	}
	if text := nexttest.JSONText(t, got); text != `{"n":1}` {
		t.Errorf("initial = %s", text)
	}
}

func TestFingerprintsAreComputedOnceFromTheSchemasStructure(t *testing.T) {
	make := func(schema contracts.TSchema) string {
		return mustDefine(t, "doc", StateOptions{
			Schema: schema, Version: 1, Initial: func() *JsonObject { return obj("n", 0.0) },
		}).Fingerprint
	}
	plain := make(contracts.Obj(contracts.Prop("n", contracts.Int())))
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(plain) {
		t.Fatalf("fingerprint %q is not 16 hex digits", plain)
	}
	described := contracts.Int()
	described.Node.(*delta.JsonObject).Set("description", "a count")
	if got := make(contracts.Obj(contracts.Prop("n", described))); got != plain {
		t.Errorf("a description changed the fingerprint: %s != %s", got, plain)
	}
	if got := make(contracts.Obj(contracts.Prop("n", contracts.Str()))); got == plain {
		t.Errorf("a structural change was not seen: %s", got)
	}
}

func TestMigrateMapsAnOlderValueAndChecksTheResultEachTimeItProducesOne(t *testing.T) {
	meter := nexttest.Metered(t)
	doc := mustDefine(t, "doc", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 2,
		Initial: func() *JsonObject { return obj("n", 0.0) },
		Migrate: func(old *JsonObject, from int) (*JsonObject, error) {
			// (old.count as number) * from: a string count is NaN, as JavaScript multiplies it.
			count, ok := old.Value("count").(float64)
			if !ok {
				return obj("n", math.NaN()), nil
			}
			return obj("n", count*float64(from)), nil
		},
	})
	got, err := doc.Migrate(obj("count", 3.0), 1, meter.Context)
	if err != nil {
		t.Fatal(err)
	}
	if text := nexttest.JSONText(t, got); text != `{"n":3}` {
		t.Errorf("migrated = %s", text)
	}
	_, err = doc.Migrate(obj("count", "x"), 1, meter.Context)
	if err == nil || !strings.Contains(err.Error(), "doc: migrate() from version 1 does not match the schema") {
		t.Errorf("err = %v", err)
	}
	if got := meter.Counts()["chord.data.validations"]; got != 2 {
		t.Errorf("validations = %v, want 2", got)
	}
}

func TestMigrateRefusesAnOlderValueWithoutAMigrate(t *testing.T) {
	doc := mustDefine(t, "doc", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 2,
		Initial: func() *JsonObject { return obj("n", 0.0) },
	})
	_, err := doc.Migrate(obj("n", 1.0), 1, background)
	if err == nil || !strings.Contains(err.Error(), "doc: no migrate from version 1 to 2") {
		t.Errorf("err = %v", err)
	}
}

// A definition is a package-level value shared by every host of a process (the Go hosts run on their own goroutines, Pi's on
// one thread): Initial's first check must be safe to race.
func TestStateDefinitionInitialIsSafeToShareBetweenHosts(t *testing.T) {
	definition, err := DefineState("race.doc", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *JsonObject { return delta.JsonObjectOf("n", float64(0)) },
	})
	if err != nil {
		t.Fatalf("DefineState: %v", err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 50 {
				if _, err := definition.Initial(background); err != nil {
					t.Errorf("Initial: %v", err)
					return
				}
			}
		})
	}
	group.Wait()
}
