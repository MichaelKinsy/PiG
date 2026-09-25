package subprocess

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestNodeModelRegistryPreservesModelIdentityAndDoesNotFabricateModels(t *testing.T) {
	shortSockDir(t)

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	model := map[string]any{
		"id":       "openrouter/org/model/name",
		"modelId":  "org/model/name",
		"provider": map[string]any{"id": "openrouter"},
		"name":     "Model Name",
		"api":      "openai-responses",
	}
	bridge.SetHostAction("getModelInfo", func() map[string]any { return model })
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "node-model-registry",
		Source:  filepath.Join("testdata", "node-model-registry.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	bridge.SetHostAction("getModels", func() []map[string]any { return []map[string]any{model} })

	command, ok := ext.Commands["model_registry_identity"]
	if !ok {
		t.Fatal("model_registry_identity command not registered")
	}
	if err := command.Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
}
