package ai

import "encoding/json"

// mustStoredModels decodes persisted catalog records for a test fixture.
func mustStoredModels(records []json.RawMessage) []AnyModel {
	models, err := DecodeStoredModels(records)
	if err != nil {
		panic(err)
	}
	return models
}
