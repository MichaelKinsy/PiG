package ai

import (
	"errors"
	"testing"
)

// pi-messages.ts:98-108 PiMessagesResponseError and utils/models-error.ts:5-13 ModelsError: constructors set `name`; a ModelsError keeps its cause
// (Unwrap, as D103 maps `cause`) and appends its detail to the message.
func TestPiMessagesResponseErrorAndModelsErrorCarryTheirNames(t *testing.T) {
	piErr := NewPiMessagesResponseError("backend said no", "bad_request", map[string]any{"k": "v"})
	if piErr.Name() != "PiMessagesResponseError" || piErr.Error() != "backend said no" || piErr.DiagnosticCode() != "bad_request" {
		t.Fatalf("name=%q message=%q code=%v", piErr.Name(), piErr.Error(), piErr.DiagnosticCode())
	}
	cause := errors.New("socket closed")
	modelsErr := NewModelsError(ModelsErrorProvider, "provider failed", cause)
	if modelsErr.Name() != "ModelsError" || modelsErr.Error() != "provider failed: socket closed" || !errors.Is(modelsErr, cause) {
		t.Fatalf("name=%q message=%q", modelsErr.Name(), modelsErr.Error())
	}
}
