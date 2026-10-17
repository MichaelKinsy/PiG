package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// rpc-mode.ts:470-478 awaits session.setModel, whose first step is `await modelRuntime.checkAuth(provider)` (agent-session.ts:2477-2480).
// A set_model that shares an input batch with a later command therefore applies after that command's synchronous prefix: the pipelined
// cycle_model cycles from the previous model and answers first, then set_model applies (comb-audit row 21, measured against Pi 1.1.0).
func TestRPCSetModelAppliesAfterTheBatchesLaterCommands(t *testing.T) {
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
	if _, err := io.WriteString(p.stdin, `{"id":"set","type":"set_model","provider":"fixture","modelId":"model-three"}`+"\n"+`{"id":"cycle","type":"cycle_model"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	var order []string
	modelOf := map[string]string{}
	p.await("both responses", func(record rpcRecord) bool {
		if record["type"] != "response" {
			return false
		}
		id, _ := record["id"].(string)
		if record["success"] != true {
			t.Fatalf("%s failed: %v", id, record)
		}
		data, _ := record["data"].(map[string]any)
		if id == "cycle" {
			data, _ = data["model"].(map[string]any)
		}
		modelOf[id], _ = data["id"].(string)
		order = append(order, id)
		return len(order) == 2
	})
	if order[0] != "cycle" || order[1] != "set" {
		t.Errorf("response order=%v, want [cycle set]", order)
	}
	if modelOf["cycle"] != "model-two" {
		t.Errorf("cycle_model cycled to %q, want model-two (from the previous model)", modelOf["cycle"])
	}
	p.sendJSON(map[string]any{"id": "state", "type": "get_state"})
	p.await("state", func(record rpcRecord) bool {
		if record["id"] != "state" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		model, _ := data["model"].(map[string]any)
		if model["id"] != "model-three" {
			t.Errorf("final model=%v, want model-three", model["id"])
		}
		return true
	})
}
