package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Pi's RPC set_model resolves the model from the current Session's model runtime (rpc-mode.ts set_model: session.modelRuntime.getAvailable) and answers its model object. A new_session before it
// builds the next Session's extensions and registry, and the model it selects carries what the extension registered. Issue #522 is the interactive mode's twin of this path: RPC reads the runtime's
// current services on every command, so a replacement does not leave it a startup builder.
func TestRPCSetModelAfterNewSessionKeepsRegisteredMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "model-after-new.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "replace.log")
	p := startRPCProcess(t, []string{
		"PIG_TEST_FAUX=1", "PIG_HOME=" + dir, "PIG_CODING_AGENT_DIR=" + filepath.Join(dir, "agent"), "PIG_TEST_REPLACE_LOG=" + log,
		"PIG_TEST_MODEL_AFTER_NEW_URL=http://127.0.0.1:9/v1",
	}, "--offline", "-e", fixture, "--model", "metaprov/model-a", "--thinking", "medium", "--session-dir", filepath.Join(dir, "sessions"))
	awaitResponse := func(id string) rpcRecord {
		t.Helper()
		var response rpcRecord
		p.await("the response "+id, func(record rpcRecord) bool {
			if record["type"] != "response" || record["id"] != id {
				return false
			}
			response = record
			return true
		})
		if response["success"] != true {
			t.Fatalf("%s failed: %v", id, response)
		}
		return response
	}
	p.sendJSON(map[string]any{"id": "1", "type": "new_session"})
	awaitResponse("1")
	p.sendJSON(map[string]any{"id": "2", "type": "set_model", "provider": "metaprov", "modelId": "model-b"})
	set, _ := awaitResponse("2")["data"].(map[string]any)
	want := registeredModels["metaprov/model-b"]
	if set["contextWindow"] != float64(want.ContextWindow) || set["maxTokens"] != float64(want.MaxTokens) || set["reasoning"] != want.Reasoning {
		t.Errorf("set_model after new_session answered %v, want the registered window %d, output limit %d and reasoning", set, want.ContextWindow, want.MaxTokens)
	}
	p.sendJSON(map[string]any{"id": "3", "type": "get_state"})
	state, _ := awaitResponse("3")["data"].(map[string]any)
	model, _ := state["model"].(map[string]any)
	if model["contextWindow"] != float64(want.ContextWindow) || state["thinkingLevel"] != "medium" {
		t.Errorf("get_state after set_model reports the model %v at thinking %v, want the window %d at medium", model, state["thinkingLevel"], want.ContextWindow)
	}
	// The extension of the replacement Session saw the same model.
	deadline := time.Now().Add(p.budget)
	for {
		var selected *modelAfterReplacementRecord
		records := readModelAfterReplacementRecords(t, log)
		for i := range records {
			if records[i].Event == "model_select" && records[i].Source == "set" && records[i].Model != nil && records[i].Model.ID == "metaprov/model-b" {
				selected = &records[i]
			}
		}
		if selected != nil {
			if gotJSON, wantJSON := marshalModelFields(t, *selected.Model), marshalModelFields(t, want); gotJSON != wantJSON || selected.Thinking != "medium" {
				t.Errorf("the extension's model_select carried %s at thinking %q, want the registered %s at medium", gotJSON, selected.Thinking, wantJSON)
			}
			return
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(log)
			t.Fatalf("the extension never recorded the model_select of metaprov/model-b; log:\n%s", data)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
