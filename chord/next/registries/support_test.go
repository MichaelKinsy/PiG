package registries_test

import (
	"fmt"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Shared helpers of the registries tests. Upstream's cases run on a host (`loadedPlugins`, `host.serve`,
// `self.contribute`); until the host package exists these tests drive the same registry layer directly, on a batch
// of their own: a record per registry name (`ChordHost.registryRecord`), contributions made as `contributionOf`
// makes them, and changes made inside a batch commit.

// env is one host's worth of registry state: its batch, its records and what they reported to `onError`.
type env struct {
	t       *testing.T
	batch   *state.Batch
	meter   *nexttest.Meter
	errors  []registries.RegistryError
	records map[string]*registries.RegistryRecord
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, meter: nexttest.Metered(t), records: map[string]*registries.RegistryRecord{}}
	e.batch = state.NewBatch(func(err state.BatchError) { t.Errorf("batch error: %v", err.Error) }, chordctx.Telemetry(e.meter.Context).Metrics())
	return e
}

// record is `ChordHost.registryRecord(name)`: made by its first use.
func (e *env) record(name string) *registries.RegistryRecord {
	if record, ok := e.records[name]; ok {
		return record
	}
	record := registries.NewRegistryRecord(name, e.batch, func(err registries.RegistryError) { e.errors = append(e.errors, err) })
	e.records[name] = record
	return record
}

// change runs work inside one batch commit, as a commit's adoptions run.
func (e *env) change(work func()) {
	e.t.Helper()
	err := e.batch.Commit(func() error {
		work()
		return nil
	}, chordctx.Background())
	if err != nil {
		e.t.Fatalf("commit: %v", err)
	}
}

// serve serves record with a typed provider, as `host.serve(R, provider)` does.
func serve[C, V any](e *env, reg registries.Registry[C, V], provider registries.RegistryProvider[C, V]) *registries.RegistryRecord {
	e.t.Helper()
	record := e.record(reg.Name)
	if err := registries.CheckProvider(reg.Name, provider.Erase()); err != nil {
		e.t.Fatalf("serve %s: %v", reg.Name, err)
	}
	e.change(func() { record.Serve(reg.Version, provider.Erase()) })
	return record
}

// contribute adds a contribution of `content` to reg's record inside a commit, as an object going live does.
func contribute[C, V any](e *env, reg registries.Registry[C, V], plugin, typ string, content registries.ContributionContent) *registries.RegistryContribution {
	e.t.Helper()
	record := e.record(reg.Name)
	contribution := registries.NewRegistryContribution(record, registries.Contributor{Plugin: plugin, Type: typ}, reg.Version, content)
	e.change(func() { record.Add(contribution) })
	return contribution
}

func plain(value any) registries.ContributionContent {
	return registries.ContributionContent{Kind: registries.ContributionPlain, Value: value}
}

func mustDefine[C, V any](t *testing.T, name string, version int) registries.Registry[C, V] {
	t.Helper()
	reg, err := registries.DefineRegistry[C, V](name, registries.DefineRegistryOptions{Version: version})
	if err != nil {
		t.Fatalf("DefineRegistry(%s): %v", name, err)
	}
	return reg
}

// cell is a changing value a contribution follows: a Chord JSON source and a native source in one (a writable, a
// derive). `set` changes it inside a batch and marks its dependents; `fail` makes it throw `err` until it is set.
type cell struct {
	dependents state.DependentSet
	value      any
	err        error
}

func (c *cell) DependentSet() *state.DependentSet { return &c.dependents }
func (c *cell) DeriveValue() (any, error)         { return c.value, c.err }
func (c *cell) CurrentAny() (any, error)          { return c.value, c.err }
func (c *cell) Value() (delta.JsonValue, error)   { return c.value, c.err }

func newCell(value any) *cell { return &cell{value: value} }

// set changes the cell as a commit does: its dependents are marked.
func (c *cell) set(e *env, value any, err error) {
	e.t.Helper()
	e.change(func() {
		c.value, c.err = value, err
		for _, dependent := range c.dependents.Snapshot() {
			dependent.Invalidate()
		}
	})
}

// json is a cell followed as a Chord JSON watchable; native as a native source.
func (c *cell) json() registries.ContributionContent {
	return registries.ContributionContent{Kind: registries.ContributionJSON, Source: c}
}

func (c *cell) native() registries.ContributionContent {
	return registries.ContributionContent{Kind: registries.ContributionNative, Native: c}
}

// valueOf is a start's value; the test fails when it is a failure.
func valueOf(t *testing.T, start state.NativeStart[any], err error) any {
	t.Helper()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if start.Kind != state.StartValue {
		t.Fatalf("start failed: %v", start.Error)
	}
	return start.Value
}

func startOf(t *testing.T, record *registries.RegistryRecord) state.NativeStart[any] {
	t.Helper()
	start, err := record.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return start
}

func composed(t *testing.T, record *registries.RegistryRecord) any {
	t.Helper()
	start, err := record.Start()
	return valueOf(t, start, err)
}

func kinds(errs []registries.RegistryError) []string {
	out := make([]string, len(errs))
	for i, err := range errs {
		out[i] = string(err.Kind)
	}
	return out
}

// listed is the composition of `Names`: the table as `plugin:type=value` strings.
func listed(table registries.RegistryTable[string], _ registries.ComposeContext) ([]string, error) {
	out := make([]string, len(table))
	for i, entry := range table {
		out[i] = fmt.Sprintf("%s:%s=%s", entry.Plugin, entry.Type, entry.Value)
	}
	return out, nil
}
