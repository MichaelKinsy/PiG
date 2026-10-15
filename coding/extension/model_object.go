package extension

import (
	"encoding/json"

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
		defaultInput := typed.Input == nil && ai.CatalogRecordOf(typed) == ""
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

// MarshalModelObjects is the JSON array of the [ModelObject] of each model, skipping nil models. The objects are joined as
// they are, so no member is escaped again (JSON.stringify escapes no <, > or &).
func MarshalModelObjects[M ai.AnyModel](models []M) ([]byte, error) {
	out := []byte{'['}
	for _, model := range models {
		object, err := ModelObject(model)
		if err != nil {
			return nil, err
		}
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
