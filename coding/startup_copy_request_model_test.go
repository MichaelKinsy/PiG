package coding

// pi: packages/coding-agent/src/core/model-runtime.ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's request uses the model the Session holds (model-runtime.ts:667-689 prepareRequest). For an id no catalog carries that model is the resolver's
// copy of one of the provider's models (BuildFallbackModel). Where the registry resolves no credentials PiG builds the request's model itself, and
// built again by provider and id the copy became the provider's connection alone: the request asked for one token (`max_completion_tokens: 1`) and
// ignored the compat flags while the Session reported the copy. The test builds that request model for a provider that has a key, so it says
// nothing about which providers may send a request without credentials.
func TestUnauthenticatedRequestModelOfAStartupCopyIsTheCopy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	config := `{"providers":{"filed":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"registry-key","models":[` +
		`{"id":"chat-1","name":"Chat 1","reasoning":true,"input":["text","image"],"contextWindow":48000,"maxTokens":3000,` +
		`"cost":{"input":3,"output":4,"cacheRead":0.3,"cacheWrite":0.4},"compat":{"supportsDeveloperRole":false,"maxTokensField":"max_tokens"}}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	runtime := services.ModelRuntime()
	if refreshed := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted {
		t.Fatalf("refresh=%v", refreshed)
	}
	listed := runtime.GetModel("filed", "chat-1")
	if listed == nil {
		t.Fatal("the registry lists no filed/chat-1")
	}
	// The resolver's copy: the provider's model under the id and name the command line gave (model-resolver.ts buildFallbackModel).
	fallback := new(*listed)
	fallback.ID, fallback.DisplayName = "nosuch", "nosuch"
	model, err := BuildFallbackModel(fallback, services)
	if err != nil {
		t.Fatal(err)
	}
	request, err := buildUnauthenticatedRequestModel(model, runtime.GetModel("filed", "nosuch"), runtime.modelObjectOrigin(model) != nil, services, "")
	if err != nil {
		t.Fatal(err)
	}

	members := func(model *ai.Model) string {
		t.Helper()
		raw, err := extension.ModelObject(model)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		picked, err := json.Marshal(map[string]any{"id": object["id"], "contextWindow": object["contextWindow"], "maxTokens": object["maxTokens"],
			"cost": object["cost"], "input": object["input"], "compat": object["compat"], "reasoning": object["reasoning"]})
		if err != nil {
			t.Fatal(err)
		}
		return string(picked)
	}
	want := `{"compat":{"maxTokensField":"max_tokens","supportsDeveloperRole":false},"contextWindow":48000,"cost":{"cacheRead":0.3,"cacheWrite":0.4,"input":3,"output":4},` +
		`"id":"nosuch","input":["text","image"],"maxTokens":3000,"reasoning":true}`
	if got := members(model); got != want {
		t.Fatalf("the Session's model of filed/nosuch:\n got %s\nwant %s", got, want)
	}
	if got := members(request); got != want {
		t.Fatalf("the request's model of filed/nosuch:\n got %s\nwant %s", got, want)
	}
}
