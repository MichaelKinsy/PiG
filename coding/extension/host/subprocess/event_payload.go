package subprocess

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// wireEventPayload projects the Go-native models an event carries onto Pi's extension-facing Model shape. Upstream hands extensions the Session's Model objects (agent-session.ts:2372-2384 _emitModelSelect).
func wireEventPayload(event any) any {
	selected, ok := event.(extension.ModelSelectEvent)
	if !ok {
		return event
	}
	selected.Model = wireModel(selected.Model)
	selected.PreviousModel = wireModel(selected.PreviousModel)
	return selected
}

// wireModel returns an untyped nil for an absent model so the omitted previousModel stays undefined.
func wireModel(model extension.Model) extension.Model {
	native, ok := model.(*ai.Model)
	if !ok {
		return model
	}
	if native == nil {
		return nil
	}
	return extension.ModelInfo(native)
}
