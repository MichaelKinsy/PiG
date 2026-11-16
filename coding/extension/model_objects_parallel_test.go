package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// withConversion replaces the conversion MarshalModelObjects calls for the test and runs it with four processors.
func withConversion(t *testing.T, convert func(ai.AnyModel) (json.RawMessage, error)) {
	t.Helper()
	previous := runtime.GOMAXPROCS(4)
	original := modelObjectOf
	modelObjectOf = convert
	t.Cleanup(func() {
		modelObjectOf = original
		runtime.GOMAXPROCS(previous)
	})
}

func composedModels(count int) []*ai.Model {
	models := make([]*ai.Model, count)
	for i := range models {
		models[i] = &ai.Model{ID: fmt.Sprintf("model-%04d", i), DisplayName: fmt.Sprintf("Model %d", i), ProviderMeta: ai.ProviderMetadata{ProviderID: "custom", API: ai.APIOpenAICompletions}}
	}
	return models
}

// The first publication of the built-in catalog converts about 2,500 models, and the conversions are independent: a list as long as a catalog is converted by
// more than one goroutine. Two conversions must be in flight together; a conversion that waits for another one, and times out when none comes, fails the test.
func TestMarshalModelObjectsConvertsALongListConcurrently(t *testing.T) {
	var inFlight, peak atomic.Int32
	met := make(chan struct{})
	var once sync.Once
	withConversion(t, func(model ai.AnyModel) (json.RawMessage, error) {
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			seen := peak.Load()
			if now <= seen || peak.CompareAndSwap(seen, now) {
				break
			}
		}
		if now >= 2 {
			once.Do(func() { close(met) })
		}
		select {
		case <-met:
		case <-time.After(5 * time.Second):
			once.Do(func() { close(met) })
		}
		return ModelObject(model)
	})
	if _, err := MarshalModelObjects(composedModels(8 * parallelModelObjectsMin)); err != nil {
		t.Fatal(err)
	}
	if peak.Load() < 2 {
		t.Fatalf("%d conversions were in flight at once, want at least 2: the list was converted by one goroutine", peak.Load())
	}
}

// The array is the one a single goroutine writes: the models in list order, a nil model left out.
func TestMarshalModelObjectsKeepsListOrderAndSkipsNilModels(t *testing.T) {
	withConversion(t, ModelObject)
	for _, count := range []int{0, 1, parallelModelObjectsMin - 1, parallelModelObjectsMin, 3*parallelModelObjectsMin + 5} {
		models := composedModels(count)
		var want []string
		for i := range models {
			if i%7 == 3 {
				models[i] = nil
				continue
			}
			want = append(want, modelObjectString(t, models[i]))
		}
		got, err := MarshalModelObjects(models)
		if err != nil {
			t.Fatalf("%d models: %v", count, err)
		}
		if expected := "[" + strings.Join(want, ",") + "]"; string(got) != expected {
			t.Fatalf("%d models: the array differs from the one a single goroutine writes", count)
		}
		if !json.Valid(got) {
			t.Fatalf("%d models: not JSON", count)
		}
	}
}

// A failure is the first one in list order, whichever goroutine reaches it first, and no array comes with it.
func TestMarshalModelObjectsReportsTheFirstFailureInListOrder(t *testing.T) {
	failure := func(id string) error { return errors.New("cannot convert " + id) }
	later := make(chan struct{})
	withConversion(t, func(model ai.AnyModel) (json.RawMessage, error) {
		switch id := model.(*ai.Model).ID; id {
		case "model-0300":
			close(later)
			return nil, failure(id)
		case "model-0100":
			// The later failure is reached first; the report must still be the earlier one. A single goroutine never reaches the later one, so it gives up.
			select {
			case <-later:
			case <-time.After(5 * time.Second):
			}
			return nil, failure(id)
		}
		return ModelObject(model)
	})
	out, err := MarshalModelObjects(composedModels(400))
	if err == nil || err.Error() != failure("model-0100").Error() {
		t.Fatalf("error %v, want the failure of model-0100", err)
	}
	if out != nil {
		t.Fatalf("an array of %d bytes came with the failure", len(out))
	}
}

// A start never takes more than maxModelObjectWorkers processors for the catalog, whatever the host has, and a short list is converted where it is.
func TestModelObjectWorkersAreBounded(t *testing.T) {
	for _, row := range []struct{ count, procs, want int }{
		{0, 192, 1}, {parallelModelObjectsMin - 1, 192, 1},
		{parallelModelObjectsMin, 192, parallelModelObjectsMin / modelObjectBlock},
		{2500, 192, 8}, {2500, 8, 8}, {2500, 3, 3}, {2500, 1, 1}, {2500, 0, 1},
	} {
		if got := modelObjectWorkers(row.count, row.procs); got != row.want {
			t.Errorf("%d models on %d processors: %d workers, want %d", row.count, row.procs, got, row.want)
		}
	}
}

// The array of the whole built-in catalog, converted in parallel, is the bytes one goroutine writes.
func TestMarshalModelObjectsOfTheBuiltInCatalogEqualsTheSerialJoin(t *testing.T) {
	withConversion(t, ModelObject)
	var models []*ai.Model
	var want []string
	for i := range ai.GeneratedModels {
		models = append(models, ai.GeneratedModels[i].ToModel())
		want = append(want, modelObjectString(t, models[len(models)-1]))
	}
	got, err := MarshalModelObjects(models)
	if err != nil {
		t.Fatal(err)
	}
	if expected := "[" + strings.Join(want, ",") + "]"; string(got) != expected {
		t.Fatalf("the array of %d built-in models differs from the one a single goroutine writes", len(models))
	}
}
