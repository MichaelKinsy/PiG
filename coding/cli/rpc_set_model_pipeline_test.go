package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pipelined set_model commands apply in the order they arrive. Each awaits `session.setModel(model)`, whose first step is
// `await modelRuntime.checkAuth(provider)` (rpc-mode.ts:470-478, agent-session.ts:2477-2480). The continuations resume in
// the order the awaits began, so Pi answers every set_model in input order and the last one wins. Measured against
// Pi 1.1.0 with this fixture: 30 pipelined set_models answered in order on 3 of 3 runs.
func TestRPCPipelinedSetModelsApplyInInputOrder(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	models, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/multi-model/agent/models.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agent", "models.json"), models, 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcess(t, []string{"PIG_HOME=" + home}, "--offline", "--no-session", "--no-extensions", "--model", "fixture/model-one")
	// The last command selects model-three, which is neither the start model nor the one before it.
	targets := []string{"model-one", "model-two", "model-three"}
	const count = 30
	var batch strings.Builder
	want := make([]string, count)
	for i := range count {
		want[i] = fmt.Sprintf("s%d", i)
		fmt.Fprintf(&batch, `{"id":%q,"type":"set_model","provider":"fixture","modelId":%q}`+"\n", want[i], targets[i%len(targets)])
	}
	if _, err := io.WriteString(p.stdin, batch.String()); err != nil {
		t.Fatal(err)
	}
	var order []string
	p.await("every set_model response", func(record rpcRecord) bool {
		if record["type"] != "response" {
			return false
		}
		id, _ := record["id"].(string)
		if record["success"] != true {
			t.Fatalf("%s failed: %v", id, record)
		}
		order = append(order, id)
		return len(order) == count
	})
	if !slices.Equal(order, want) {
		t.Errorf("set_model responses in order %v, want input order", order)
	}
	p.sendJSON(map[string]any{"id": "state", "type": "get_state"})
	p.await("state", func(record rpcRecord) bool {
		if record["id"] != "state" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		model, _ := data["model"].(map[string]any)
		if model["id"] != "model-three" {
			t.Errorf("final model=%v, want model-three (the last set_model)", model["id"])
		}
		return true
	})
}
