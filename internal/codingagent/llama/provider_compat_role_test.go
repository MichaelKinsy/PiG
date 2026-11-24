package llama

import (
	"encoding/json"
	"testing"
)

// Pi b82477b7b (#12134): packages/coding-agent/src/extensions/llama/provider.ts removes
// redundant supportsDeveloperRole: false from toPiModel compat now that openai-completions
// defaults supportsDeveloperRole to false for non-api.openai.com endpoints.
func TestToPiModelOmitsRedundantSupportsDeveloperRole(t *testing.T) {
	info := LlamaModelInfo{
		ID: "qwen3.8-27b",
	}
	model := toPiModel(info, "http://127.0.0.1:9931", nil, 0)
	data, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal model: %v", err)
	}
	compat, ok := raw["compat"].(map[string]any)
	if !ok {
		t.Fatalf("compat missing or not object: %s", data)
	}
	if v, exists := compat["supportsDeveloperRole"]; exists {
		t.Fatalf("toPiModel compat unexpectedly includes supportsDeveloperRole=%v; Pi b82477b7b removed it", v)
	}
	if compat["supportsStore"] != false || compat["supportsReasoningEffort"] != false || compat["supportsUsageInStreaming"] != true || compat["supportsStrictMode"] != false || compat["maxTokensField"] != "max_tokens" {
		t.Fatalf("unexpected compat fields: %v", compat)
	}
}
