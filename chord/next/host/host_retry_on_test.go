package host

import (
	"errors"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/host/retry-on.test.ts for the part that needs no host. Every case of upstream's file runs
// a host (loaded, host.hold, serve with self.retryOn and self.open, host.close): the retries of a failed object, the wake
// remembered while it activates, wakes while live or parked, the collection of the object's wakes, and the contributions
// leaving. These are own tests of retrySource and retryWakes on a registry record ("app.models", composing the sum of the
// contributions, failing for a negative one, as serveModels does), each naming the case whose behavior it checks.

// modelsEnv is the `app.models` registry of retry-on.test.ts on a batch of its own, and its value as a reader of version 1
// sees it.
type modelsEnv struct {
	t             *testing.T
	batch         *state.Batch
	record        *registries.RegistryRecord
	source        *registries.RegistryValueSource
	contributions *registries.RegistryContribution
}

func newModelsEnv(t *testing.T) *modelsEnv {
	t.Helper()
	e := &modelsEnv{t: t}
	e.batch = state.NewBatch(func(err state.BatchError) { t.Errorf("batch reported %s: %v", err.Kind, err.Error) }, nil)
	e.record = registries.NewRegistryRecord("app.models", e.batch, func(registries.RegistryError) {})
	err := e.record.Serve(1, registries.RegistryProvider[any, any]{
		Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
			sum := 0
			for _, entry := range table {
				sum += entry.Value.(int)
			}
			if sum < 0 {
				return nil, errors.New("negative")
			}
			return sum, nil
		},
	})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	e.source = e.record.Value(1)
	return e
}

// set is the models contributed changing to n (the Runtime object's `count.set(n)`), inside a commit.
func (e *modelsEnv) set(n int) {
	e.t.Helper()
	err := e.batch.Commit(func() error {
		if e.contributions != nil {
			e.record.Remove(e.contributions)
		}
		e.contributions = registries.NewRegistryContribution(e.record, registries.Contributor{Plugin: "@test/core", Type: "app.runtime"}, 1,
			registries.ContributionContent{Kind: registries.ContributionPlain, Value: n})
		e.record.Add(e.contributions)
		return nil
	}, chordctx.Background())
	if err != nil {
		e.t.Fatalf("commit: %v", err)
	}
}

// retry is a retryOn source of the registry value.
func (e *modelsEnv) retry() retrySourceType {
	e.t.Helper()
	source, err := retrySource(e.source, "app.user(1)")
	if err != nil {
		e.t.Fatalf("retrySource: %v", err)
	}
	return source
}

func TestRetrySourceTakesRegistryValuesAndConstants(t *testing.T) {
	e := newModelsEnv(t)
	if source, err := retrySource(e.source, "app.user(1)"); err != nil || source.value != e.source || source.constant != nil {
		t.Fatalf("retrySource(registry value) = %+v, %v", source, err)
	}
	constant := state.NewConstant(1.0)
	if source, err := retrySource(constant, "app.user(1)"); err != nil || source.constant != constant || source.value != nil {
		t.Fatalf("retrySource(constant) = %+v, %v", source, err)
	}
}

// Mirrors "refuses sources that end with an attempt: a reference's member, its own writable, `ref.state`".
func TestRetrySourceRefusesSourcesThatEndWithAnAttempt(t *testing.T) {
	e := newModelsEnv(t)
	loop := newFakeLoop()
	member := newActivity(loop, e.batch)
	refState := newReferenceState(func() *RefState { return refLive }, e.batch, nil)
	derive := state.NewDerive(nil, nil, e.batch)
	var nilValue *registries.RegistryValueSource
	var nilConstant *state.Constant
	for name, source := range map[string]any{"a member": member, "ref.state": refState, "a derive": derive, "nil": nil, "a nil registry value": nilValue, "a nil constant": nilConstant, "a string": "x"} {
		_, err := retrySource(source, "app.picky(0)")
		want := "app.picky(0): self.retryOn takes registry values and constants (sources that outlive the attempt)"
		if err == nil || err.Error() != want {
			t.Errorf("retrySource(%s) error = %v, want %q", name, err, want)
		}
	}
}

// Mirrors "retries a failed object when a source gets a new value": each new value of a followed source wakes, inside the
// batch that made it.
func TestRetryWakesWakeOnEachNewValue(t *testing.T) {
	e := newModelsEnv(t)
	wakes := &retryWakes{}
	if wakes.during != nil || wakes.leaving {
		t.Fatalf("a fresh retryWakes has during %v leaving %v", wakes.during, wakes.leaving)
	}
	count := 0
	wakes.replace([]retrySourceType{e.retry()}, func() { count++ })
	e.set(1)
	if count != 1 {
		t.Fatalf("wakes after the first value = %d, want 1", count)
	}
	e.set(2)
	if count != 2 {
		t.Fatalf("wakes after the second value = %d, want 2", count)
	}
}

// Mirrors "wakes on value calls only: a throwing compose wakes nothing, its next good composition does".
func TestRetryWakesOnValueCallsOnly(t *testing.T) {
	e := newModelsEnv(t)
	wakes := &retryWakes{}
	count := 0
	wakes.replace([]retrySourceType{e.retry()}, func() { count++ })
	e.set(-1)
	if count != 0 {
		t.Fatalf("wakes after a failing compose = %d, want 0", count)
	}
	e.set(2)
	if count != 1 {
		t.Fatalf("wakes after the next good composition = %d, want 1", count)
	}
}

// Mirrors the declaration of each attempt replacing the last one ("replaced by each attempt's declaration"): the previous
// set stops, constants follow nothing, and `stop` ends every follow.
func TestRetryWakesReplaceAndStop(t *testing.T) {
	e := newModelsEnv(t)
	wakes := &retryWakes{}
	var first, second int
	wakes.replace([]retrySourceType{e.retry()}, func() { first++ })
	wakes.replace([]retrySourceType{e.retry(), {constant: state.NewConstant(1.0)}}, func() { second++ })
	if len(wakes.subscribed) != 1 {
		t.Fatalf("subscriptions = %d, want 1 (a constant is followed by nothing)", len(wakes.subscribed))
	}
	e.set(1)
	if first != 0 || second != 1 {
		t.Fatalf("wakes first %d second %d, want 0 1", first, second)
	}
	wakes.stop()
	if len(wakes.subscribed) != 0 {
		t.Fatalf("subscriptions after stop = %d, want 0", len(wakes.subscribed))
	}
	e.set(2)
	if second != 1 {
		t.Fatalf("wakes after stop = %d, want 1", second)
	}
	// Stopping again, or replacing with nothing, is harmless.
	wakes.stop()
	wakes.replace(nil, func() { second++ })
	e.set(3)
	if second != 1 {
		t.Fatalf("wakes after an empty declaration = %d, want 1", second)
	}
}

// Own test: one observer follows every source of a declaration, including the same source twice, and stop leaves nothing
// followed.
func TestRetryWakesTheSameSourceTwice(t *testing.T) {
	e := newModelsEnv(t)
	wakes := &retryWakes{}
	count := 0
	wakes.replace([]retrySourceType{e.retry(), e.retry()}, func() { count++ })
	e.set(1)
	if count != 1 {
		t.Fatalf("wakes = %d, want 1", count)
	}
	wakes.stop()
	if e.source.Watched() {
		t.Fatal("the source is still followed after stop")
	}
}
