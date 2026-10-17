package extensionconformance

import "encoding/json"

// modelJSON is a test model object as the host sends it: the JSON of model.
func modelJSON(model map[string]any) json.RawMessage {
	data, err := json.Marshal(model)
	if err != nil {
		panic(err)
	}
	return data
}

// modelJSONs is modelJSON of each model.
func modelJSONs(models ...map[string]any) []json.RawMessage {
	out := make([]json.RawMessage, len(models))
	for i, model := range models {
		out[i] = modelJSON(model)
	}
	return out
}
