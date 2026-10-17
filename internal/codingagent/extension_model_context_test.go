package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestInprocExtensionContextReadsCurrentModel(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{Name: "model-reader"}}, t.TempDir())
	mode := &InteractiveMode{
		newRunner: runner,
		opts:      InteractiveModeOptions{},
	}
	mode.wireInprocContextActions()
	ctx := runner.CreateCommandContext()

	model, err := ctx.Model()
	if err != nil {
		t.Fatal(err)
	}
	if model != nil {
		t.Fatalf("model before selection = %#v, want nil", model)
	}

	// upstream: runner.ts:906-909 `get model()` returns getModel(), the Session's own Model object (agent-session.ts:3437), with every member an extension reads (provider, api), not an id and display-name projection.
	selected := &ai.Model{ID: "selected-model", DisplayName: "Selected Model", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture", API: "openai-responses"}}
	mode.opts.Model = selected
	model, err = ctx.Model()
	if err != nil {
		t.Fatal(err)
	}
	// upstream: types.ts ExtensionContext.model is the Session's Model object.
	if model != mode.opts.Model || model.ID != "selected-model" || model.DisplayName != "Selected Model" {
		t.Fatalf("model = %#v, want the Session's model", model)
	}
}
