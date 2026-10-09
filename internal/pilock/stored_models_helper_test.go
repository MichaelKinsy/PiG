package pilock_test

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// mustStoredModels decodes persisted catalog records for a test fixture.
func mustStoredModels(records []json.RawMessage) []ai.AnyModel {
	models, err := ai.DecodeStoredModels(records)
	if err != nil {
		panic(err)
	}
	return models
}
