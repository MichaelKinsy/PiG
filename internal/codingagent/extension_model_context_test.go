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
		opts:      InteractiveOptions{},
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

	mode.opts.Model = &ai.Model{ID: "selected-model", DisplayName: "Selected Model"}
	model, err = ctx.Model()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := model.(map[string]any)
	if !ok {
		t.Fatalf("model type = %T, want map[string]any", model)
	}
	if got["id"] != "selected-model" || got["displayName"] != "Selected Model" {
		t.Fatalf("model = %#v", got)
	}
}
