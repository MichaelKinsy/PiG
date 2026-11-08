package wiring

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Registration field survival of Chord's two declared definitions (jobs.ts defineJob, table.ts defineTable): what a plugin
// declares is what the definition holds when the host registers it, field by field.

func TestJobDefinitionKeepsEveryDeclaredFieldSurvivesRegistration(t *testing.T) {
	concurrency := 3
	var definition *host.JobDefinition
	definition, err := host.DefineJob("notes.sync", host.JobOptions{
		Version:     2,
		Args:        contracts.Obj(contracts.Prop("id", contracts.Str())),
		Result:      contracts.Str(),
		Concurrency: &concurrency,
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Name != "notes.sync" || definition.Version != 2 || definition.Concurrency == nil || *definition.Concurrency != 3 {
		t.Fatalf("definition = %+v, want name notes.sync, version 2, concurrency 3", definition)
	}
	if definition.Args == nil || definition.Result == nil || definition.Fingerprint == "" || definition.State != nil || definition.Persist != nil {
		t.Fatalf("definition = %+v: args, result and fingerprint must survive; no state or persistence was declared", definition)
	}
	if _, err := host.DefineJob("notes.sync", host.JobOptions{Version: 0, Args: contracts.Obj(), Result: contracts.Str()}); err == nil {
		t.Fatal("a job of version 0 was registered")
	}
}

func TestTableDefinitionKeepsEveryDeclaredFieldSurvivesRegistration(t *testing.T) {
	var definition *store.TableDefinition
	definition, err := store.NewTableDefinition("entries", store.TableOptions{
		Version: 3,
		Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("n", store.Int())},
		Key:     []string{"id"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, column := range definition.Columns {
		names = append(names, column.Name)
	}
	if definition.Name != "entries" || definition.Version != 3 || !slices.Equal(definition.Key, []string{"id"}) || !slices.Equal(names, []string{"id", "n"}) {
		t.Fatalf("definition = name %q, version %d, key %v, columns %v", definition.Name, definition.Version, definition.Key, names)
	}
	if definition.Fingerprint == "" || definition.Evolution != nil {
		t.Fatalf("fingerprint %q, evolution %v: a base declares a fingerprint and no evolution", definition.Fingerprint, definition.Evolution)
	}
}
