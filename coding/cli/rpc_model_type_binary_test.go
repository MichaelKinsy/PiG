package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// piCatalogModelJSON is JSON.stringify of every chat model in Pi's MODELS, keyed by "provider/id": the bytes Pi's RPC
// model objects of built-in models carry.
func piCatalogModelJSON(t *testing.T) map[string]string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	const script = `
const [root] = process.argv.slice(1);
const url = require('node:url').pathToFileURL(require('node:path').join(root, 'node_modules/@earendil-works/pi-ai/dist/models.generated.js')).href;
import(url).then(({ MODELS }) => {
  const out = {};
  for (const [provider, models] of Object.entries(MODELS)) {
    for (const model of Object.values(models)) out[provider + "/" + model.id] = JSON.stringify(model);
  }
  process.stdout.write(JSON.stringify(out));
});`
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, root)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var models map[string]string
	if err := json.Unmarshal(raw, &models); err != nil {
		t.Fatal(err)
	}
	return models
}

// Pi's RPC model objects are the registry's model objects: a built-in model and a Radius baseline model carry the
// catalog's "type":"chat" (packages/ai/src/providers/data/*.json, model-catalog.ts flattenModelCatalog), and a models.json
// model has no type even when its definition writes one (provider-composer.ts:230-248 modelFromJson builds it member by
// member). This holds for the startup model in get_state as for get_available_models and set_model.
func TestRPCModelObjectsCarryTheRegistryModelType(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	agentDir := t.TempDir()
	modelsJSON := `{"providers":{"custom-typeless":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","apiKey":"sk-test","models":[{"id":"typeless-1"},{"id":"written-chat","type":"chat"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("RADIUS_API_KEY", "radius-test")
	// set_model applies its model only after `await session.setModel(model)` checks the provider's auth (rpc-mode.ts:470-478,
	// agent-session.ts:2477-2480), so a get_state in the same input batch still reports the previous model, in Pi as in
	// PiG. The get_state that reads the selected model is sent once the set_model response has arrived.
	responses := runRPCRounds(t, bin, agentDir, []string{"--mode", "rpc", "--no-session", "--offline", "--model", "openai/gpt-4o"}, [][]string{{
		`{"id":"state","type":"get_state"}`,
		`{"id":"models","type":"get_available_models"}`,
		`{"id":"custom","type":"set_model","provider":"custom-typeless","modelId":"typeless-1"}`,
	}, {
		`{"id":"custom-state","type":"get_state"}`,
		`{"id":"radius","type":"set_model","provider":"radius","modelId":"balanced"}`,
	}})
	modelType := func(id string, raw json.RawMessage) (string, bool) {
		t.Helper()
		var model map[string]any
		if err := json.Unmarshal(raw, &model); err != nil {
			t.Fatalf("%s model: %v in %s", id, err, raw)
		}
		value, ok := model["type"]
		text, _ := value.(string)
		return text, ok
	}
	stateModel := func(id string) json.RawMessage {
		var state struct {
			Model json.RawMessage `json:"model"`
		}
		if err := json.Unmarshal(responses[id], &state); err != nil || state.Model == nil {
			t.Fatalf("%s: no model in %s (%v)", id, responses[id], err)
		}
		return state.Model
	}
	if got, ok := modelType("get_state", stateModel("state")); !ok || got != "chat" {
		t.Errorf("get_state model of openai/gpt-4o: type %q (present %t), want \"chat\"", got, ok)
	}
	// Each built-in model object is Pi's catalog entry byte for byte, its own member order included.
	pi := piCatalogModelJSON(t)
	if got, want := string(stateModel("state")), pi["openai/gpt-4o"]; got != want {
		t.Errorf("get_state model of openai/gpt-4o:\n got %s\nwant %s", got, want)
	}
	var available struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(responses["models"], &available); err != nil || len(available.Models) == 0 {
		t.Fatalf("get_available_models: %s (%v)", responses["models"], err)
	}
	listed := map[string]bool{}
	for _, raw := range available.Models {
		var head struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
		}
		_ = json.Unmarshal(raw, &head)
		listed[head.Provider+"/"+head.ID] = true
		got, ok := modelType("get_available_models", raw)
		if want, builtin := pi[head.Provider+"/"+head.ID]; builtin && head.Provider != "custom-typeless" && string(raw) != want {
			t.Errorf("get_available_models %s/%s:\n got %s\nwant %s", head.Provider, head.ID, raw, want)
		}
		if head.Provider == "custom-typeless" {
			if ok {
				t.Errorf("get_available_models %s/%s: type %q, want none (a models.json model has none)", head.Provider, head.ID, got)
			}
		} else if !ok || got != "chat" {
			t.Errorf("get_available_models %s/%s: type %q (present %t), want \"chat\"", head.Provider, head.ID, got, ok)
		}
	}
	for _, id := range []string{"custom-typeless/typeless-1", "custom-typeless/written-chat", "radius/balanced"} {
		if !listed[id] {
			t.Errorf("get_available_models does not list %s", id)
		}
	}
	if got, ok := modelType("set_model", responses["radius"]); !ok || got != "chat" {
		t.Errorf("set_model radius/balanced: type %q (present %t), want \"chat\" (the Radius baseline catalog's)", got, ok)
	}
	if got, ok := modelType("set_model", responses["custom"]); ok {
		t.Errorf("set_model custom-typeless/typeless-1: type %q, want none", got)
	}
	var selected struct {
		Provider string `json:"provider"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(stateModel("custom-state"), &selected); err != nil || selected.Provider != "custom-typeless" || selected.ID != "typeless-1" {
		t.Fatalf("get_state after set_model custom-typeless/typeless-1 reports %s/%s (%v)", selected.Provider, selected.ID, err)
	}
	if got, ok := modelType("get_state", stateModel("custom-state")); ok {
		t.Errorf("get_state after set_model custom-typeless/typeless-1: type %q, want none", got)
	}
}

// runRPCRounds runs pig in RPC mode and sends each round of commands once every command of the previous round has its
// response. It returns the data of each response by command id and fails the test on a failed response.
func runRPCRounds(t *testing.T, bin, agentDir string, args []string, rounds [][]string) map[string]json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	cmd := modePigCommand(ctx, t, bin, agentDir, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	child, err := testenv.Start(t, cmd)
	if err != nil {
		t.Fatal(err)
	}
	responses := map[string]json.RawMessage{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for _, round := range rounds {
		pending := map[string]bool{}
		for _, command := range round {
			var head struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(command), &head); err != nil || head.ID == "" {
				t.Fatalf("command without an id: %s", command)
			}
			pending[head.ID] = true
			if _, err := io.WriteString(stdin, command+"\n"); err != nil {
				t.Fatalf("write %s: %v\nstderr: %s", head.ID, err, stderr.String())
			}
		}
		for len(pending) > 0 && scanner.Scan() {
			var response struct {
				ID      string          `json:"id"`
				Type    string          `json:"type"`
				Success bool            `json:"success"`
				Data    json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &response); err != nil || response.Type != "response" || response.ID == "" {
				continue
			}
			if !response.Success {
				t.Fatalf("%s failed: %s", response.ID, scanner.Text())
			}
			responses[response.ID] = response.Data
			delete(pending, response.ID)
		}
		if len(pending) > 0 {
			t.Fatalf("no response to %v (%v)\nstderr: %s", slices.Sorted(maps.Keys(pending)), scanner.Err(), stderr.String())
		}
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, stdout)
	if err := child.Wait(); err != nil {
		t.Fatalf("pig --mode rpc: %v\nstderr: %s", err, stderr.String())
	}
	return responses
}

// A built-in model that models.json changes is still Pi's catalog object after its spread merges
// (provider-composer.ts applyModelsJson, mergeCompat, applyModelOverride): the changed members keep their catalog
// place, and a merged object keeps the catalog's members first, then the added ones. Pi 1.1.0 sends exactly want.
func TestRPCOverriddenBuiltinModelKeepsTheCatalogOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	agentDir := t.TempDir()
	modelsJSON := `{"providers":{"openai":{"baseUrl":"http://127.0.0.1:9/v1","compat":{"supportsStore":false},"modelOverrides":{"gpt-4o":{"name":"Renamed","contextWindow":1000}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	stdinPath := filepath.Join(t.TempDir(), "commands.jsonl")
	if err := os.WriteFile(stdinPath, []byte(`{"id":"state","type":"get_state"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, err := os.Open(stdinPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	t.Setenv("OPENAI_API_KEY", "sk-test")
	stdout, stderr, code := runPigForModeTestIn(t, bin, agentDir, stdin, nil, "--mode", "rpc", "--no-session", "--model", "openai/gpt-4o")
	if code != 0 {
		t.Fatalf("pig --mode rpc exited %d\nstderr: %s", code, stderr)
	}
	var response struct {
		Data struct {
			Model json.RawMessage `json:"model"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &response); err != nil {
		t.Fatalf("get_state: %v in %s", err, stdout)
	}
	const want = `{"id":"gpt-4o","name":"Renamed","api":"openai-responses","provider":"openai","baseUrl":"http://127.0.0.1:9/v1","reasoning":false,"input":["text","image"],"cost":{"input":2.5,"output":10,"cacheRead":1.25,"cacheWrite":0},"contextWindow":1000,"maxTokens":16384,"compat":{"supportsStrictMode":true,"supportsStore":false},"inputLimits":{"maxRequestBytes":536870912,"images":{"maxPerRequest":1500,"resize":{"maxWidth":2000,"maxHeight":2000,"maxBytes":4718592,"jpegQuality":80}}},"type":"chat"}`
	if got := string(response.Data.Model); got != want {
		t.Fatalf("get_state model:\n got %s\nwant %s", got, want)
	}
}
