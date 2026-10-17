package subprocess

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

// modelMember decodes one member of a model object, or nil.
func modelMember(model json.RawMessage, key string) any {
	var members map[string]any
	if json.Unmarshal(model, &members) != nil {
		return nil
	}
	return members[key]
}
