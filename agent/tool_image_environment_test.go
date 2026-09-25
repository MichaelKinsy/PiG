package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestToolEnvironmentCarriesCurrentImageProfile(t *testing.T) {
	model := &ai.Model{ID: "image-model", Input: []string{"text", "image"}, InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 640}}}}
	a := NewAgent(AgentOptions{Model: model})
	env := a.toolEnvironment(model, ai.ThinkingLevel(""))
	if env.InputLimits.Images.Resize.MaxWidth != 640 || env.SupportsImages == nil || !*env.SupportsImages {
		t.Fatalf("image environment=%#v", env)
	}
	env.InputLimits.Images.Resize.MaxWidth = 1
	if model.InputLimits.Images.Resize.MaxWidth != 640 {
		t.Fatal("tool environment aliases selected model")
	}
	model.Input = []string{"text"}
	next := a.toolEnvironment(model, ai.ThinkingLevel(""))
	if next.SupportsImages == nil || *next.SupportsImages {
		t.Fatal("non-vision model not propagated")
	}
}
