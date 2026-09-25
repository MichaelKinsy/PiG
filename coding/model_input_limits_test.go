package coding

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelRuntimeInputLimitsGeneratedOverrideAndIsolation(t *testing.T) {
	dir := t.TempDir()
	config := `{"providers":{"anthropic":{"modelOverrides":{"claude-opus-5":{"inputLimits":{"images":{"resize":{"maxWidth":1000}}}}}}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("anthropic", "claude-opus-5")
	if model == nil || model.InputLimits == nil || model.InputLimits.Images == nil || model.InputLimits.Images.Resize == nil {
		t.Fatal("model input limits were dropped")
	}
	if model.InputLimits.MaxRequestBytes != 33554432 || model.InputLimits.Images.MaxPerRequest != 600 || model.InputLimits.Images.Resize.MaxWidth != 1000 {
		t.Fatalf("input limits = %+v, images = %+v", model.InputLimits, model.InputLimits.Images)
	}
	model.InputLimits.Images.Resize.MaxWidth = 1
	again := services.ModelRuntime().GetModel("anthropic", "claude-opus-5")
	if again.InputLimits.Images.Resize.MaxWidth != 1000 {
		t.Fatal("runtime model aliases configuration")
	}
}
