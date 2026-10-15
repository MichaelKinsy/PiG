package ai

import (
	"errors"
	"testing"
)

// models-error.ts:10 and pi-messages.ts:104: the constructors set `name` to the class name, so an error a caller receives reports "ModelsError" or "PiMessagesResponseError". Go reads it through Name() on the concrete class (D103: the inherited name is Name()). The constructors also keep their other arguments: ModelsError appends its cause's detail to the message and keeps the cause (Unwrap, as D103 maps `cause`), and PiMessagesResponseError keeps its message and code (pi-messages.ts:102-106).
func TestModelsErrorAndPiMessagesResponseErrorCarryTheirPiNames(t *testing.T) {
	cause := errors.New("socket closed")
	models, ok := errors.AsType[*ModelsError](NewModelsError(ModelsErrorProvider, "provider failed", cause))
	if !ok || models.Name() != "ModelsError" {
		t.Fatalf("ModelsError name = %v (ok=%v)", models, ok)
	}
	if models.Error() != "provider failed: socket closed" || models.Code != ModelsErrorProvider || !errors.Is(models, cause) {
		t.Fatalf("ModelsError message=%q code=%q cause kept=%v", models.Error(), models.Code, errors.Is(models, cause))
	}
	response, ok := errors.AsType[*PiMessagesResponseError](error(NewPiMessagesResponseError("backend said no", "bad_request", map[string]any{"k": "v"})))
	if !ok || response.Name() != "PiMessagesResponseError" {
		t.Fatalf("PiMessagesResponseError name = %v (ok=%v)", response, ok)
	}
	if response.Error() != "backend said no" || response.DiagnosticCode() != "bad_request" {
		t.Fatalf("PiMessagesResponseError message=%q code=%v", response.Error(), response.DiagnosticCode())
	}
}
