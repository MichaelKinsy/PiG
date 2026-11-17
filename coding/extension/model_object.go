package extension

import (
	"encoding/json"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
)

// ModelObject is the JSON of the model object Pi hands an extension (ctx.model, model_select, ctx.modelRegistry, a provider
// object's model argument): Pi's model object with its members in Pi's order. A model from a catalog record (the built-in
// catalog, a models store or an extension's registered definition) is ai.CatalogModelJSON, the record's members in the record's
// order; any other model is its persisted record (ai.EncodeModelsCatalog), whose members follow provider-composer.ts:230-248. A
// nil model is nil.
//
// A chat model with no provider id takes its provider's, and one without a record and with no input modalities reads as text
// (and image when it supports images), as Pi's composer defaults input to ["text"]; a record's input is its own.
func ModelObject(model ai.AnyModel) (json.RawMessage, error) {
	switch typed := model.(type) {
	case nil:
		return nil, nil
	case *ai.Model:
		if typed == nil {
			return nil, nil
		}
		defaultInput := typed.Input == nil && ai.CatalogRecordRefOf(typed) == ""
		if (typed.ProviderMeta.ProviderID == "" && typed.Provider != nil) || defaultInput {
			copied := *typed
			if copied.ProviderMeta.ProviderID == "" && copied.Provider != nil {
				copied.ProviderMeta.ProviderID = copied.Provider.ID()
			}
			if defaultInput {
				copied.Input = []string{"text"}
				if copied.Capabilities.SupportsImages {
					copied.Input = append(copied.Input, "image")
				}
			}
			model = &copied
		}
	case *ai.ImageModel:
		if typed == nil {
			return nil, nil
		}
	case *ai.ClassifierModel:
		if typed == nil {
			return nil, nil
		}
	}
	data, ok, err := ai.CatalogModelJSON(model)
	if err != nil {
		return nil, err
	}
	if ok {
		return data, nil
	}
	records, err := ai.EncodeModelsCatalog([]ai.AnyModel{model})
	if err != nil {
		return nil, err
	}
	return records[0], nil
}

// ModelValue is a model inside a larger JSON value: it encodes as the model's [ModelObject] (null for a nil model), and that
// encoding fails with ModelObject's error.
type ModelValue struct{ Model ai.AnyModel }

// MarshalJSON encodes the model's [ModelObject].
func (v ModelValue) MarshalJSON() ([]byte, error) {
	data, err := ModelObject(v.Model)
	if data == nil && err == nil {
		return []byte("null"), nil
	}
	return data, err
}

// modelObjectOf converts one model for MarshalModelObjects; it is ModelObject and is replaced only by the tests that observe the conversions.
var modelObjectOf = ModelObject

const (
	// modelObjectBlock is how many consecutive models a conversion worker takes at a time, and parallelModelObjectsMin the model count below which
	// one goroutine converts them all: the first publication of the built-in catalog converts about 2,500 models, one conversion costs 10 to 30 us.
	modelObjectBlock        = 32
	parallelModelObjectsMin = 4 * modelObjectBlock
	// pig additive (D19): the subprocess catalog frame has no counterpart in Pi, whose extensions read the registry in process.
	// maxModelObjectWorkers bounds the goroutines of one list: the catalog is about 80 blocks and 80 ms of work in all, which eight workers bring to
	// ten, so a larger fan-out gains little, and a start on a host with a hundred processors must not take every one of them for it.
	maxModelObjectWorkers = 8
)

// modelObjectWorkers is how many goroutines convert a list of count models on a host that runs procs of them at once.
func modelObjectWorkers(count, procs int) int {
	if count < parallelModelObjectsMin {
		return 1
	}
	return max(1, min(procs, maxModelObjectWorkers, (count+modelObjectBlock-1)/modelObjectBlock))
}

// MarshalModelObjects is the JSON array of the [ModelObject] of each model, skipping nil models. The objects are joined as
// they are, so no member is escaped again (JSON.stringify escapes no <, > or &).
//
// The conversions are independent, so a list of parallelModelObjectsMin models or more is converted by up to maxModelObjectWorkers goroutines (and
// no more than GOMAXPROCS), each taking blocks of models in order; the array is the one a single goroutine writes, and a failure is the first in list order.
func MarshalModelObjects[M ai.AnyModel](models []M) ([]byte, error) {
	objects := make([]json.RawMessage, len(models))
	blocks := (len(models) + modelObjectBlock - 1) / modelObjectBlock
	failures := make([]error, blocks)
	var next atomic.Int64
	convert := func() {
		for {
			block := int(next.Add(1) - 1)
			if block >= blocks {
				return
			}
			for i := block * modelObjectBlock; i < min(len(models), (block+1)*modelObjectBlock); i++ {
				object, err := modelObjectOf(models[i])
				if err != nil {
					failures[block] = err
					break
				}
				objects[i] = object
			}
		}
	}
	workers := modelObjectWorkers(len(models), runtime.GOMAXPROCS(0))
	var group sync.WaitGroup
	for range workers - 1 {
		group.Go(convert)
	}
	convert()
	group.Wait()
	for _, err := range failures {
		if err != nil {
			return nil, err
		}
	}
	size, count := len("[]"), 0
	for _, object := range objects {
		if object != nil {
			size += len(object)
			count++
		}
	}
	out := make([]byte, 1, size+max(count-1, 0))
	out[0] = '['
	for _, object := range objects {
		if object == nil {
			continue
		}
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = append(out, object...)
	}
	return append(out, ']'), nil
}
