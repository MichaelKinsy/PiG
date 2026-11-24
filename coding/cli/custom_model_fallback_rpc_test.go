package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestRPCCustomModelIDKeepsTheProvidersLimitsCostAndCompat is the caller side of TestStartupCustomModelIDIsACopyOfTheProvidersModel: a pig
// process started with `--model <models.json provider>/<unknown id>` reports the copied model in get_state, sends a request shaped by its
// compat flags and output limit, and prices the turn with its rates. The copied model sends `max_tokens` (not `max_completion_tokens`) with
// the copied model's maxTokens (3000) and system role; a model with no limits or compat sent a one-token limit in `max_completion_tokens`.
func TestRPCCustomModelIDKeepsTheProvidersLimitsCostAndCompat(t *testing.T) {
	serverURL, requests := customModelCompletionsServer(t)
	config := `{"providers":{"configured":{"baseUrl":"` + serverURL + `/v1","apiKey":"unused","api":"openai-completions","models":[` +
		`{"id":"configured-1","name":"Configured One","reasoning":true,"input":["text","image"],"contextWindow":48000,"maxTokens":3000,` +
		`"cost":{"input":3,"output":4,"cacheRead":0.3,"cacheWrite":0.4},"compat":{"supportsDeveloperRole":false,"maxTokensField":"max_tokens"}}]}}}`
	p := startRPCProcessAt(t, t.TempDir(), customModelHomeEnv(t, config), "--no-session", "--model", "configured/nosuch")

	p.send(`{"id":"state","type":"get_state"}`)
	p.await("state of the custom model", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "state" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		model, _ := data["model"].(map[string]any)
		got, err := json.Marshal(map[string]any{"id": model["id"], "name": model["name"], "provider": model["provider"], "contextWindow": model["contextWindow"],
			"maxTokens": model["maxTokens"], "cost": model["cost"], "input": model["input"], "compat": model["compat"], "reasoning": model["reasoning"]})
		if err != nil {
			t.Fatal(err)
		}
		want := `{"compat":{"maxTokensField":"max_tokens","supportsDeveloperRole":false},"contextWindow":48000,"cost":{"cacheRead":0.3,"cacheWrite":0.4,"input":3,"output":4},` +
			`"id":"nosuch","input":["text","image"],"maxTokens":3000,"name":"nosuch","provider":"configured","reasoning":true}`
		if string(got) != want {
			t.Fatalf("get_state model of configured/nosuch:\n got %s\nwant %s", got, want)
		}
		return true
	})

	p.send(`{"id":"prompt","type":"prompt","message":"hello"}`)
	p.await("the turn's assistant message", func(record rpcRecord) bool {
		message, _ := record["message"].(map[string]any)
		if record["type"] != "message_end" || message["role"] != "assistant" {
			return false
		}
		usage, _ := message["usage"].(map[string]any)
		cost, _ := usage["cost"].(map[string]any)
		// 1000 input tokens at 3 and 500 output tokens at 4 per million.
		for member, want := range map[string]float64{"input": 0.003, "output": 0.002, "total": 0.005} {
			if got, ok := cost[member].(float64); !ok || math.Abs(got-want) > 1e-12 {
				t.Fatalf("usage.cost.%s = %v, want %v (usage %v)", member, cost[member], want, usage)
			}
		}
		return true
	})
	p.closeAndWait("after the turn")

	select {
	case body := <-requests:
		messages, _ := body["messages"].([]any)
		first, _ := messages[0].(map[string]any)
		if body["model"] != "nosuch" || body["max_tokens"] != float64(3000) || body["max_completion_tokens"] != nil || first["role"] != "system" {
			t.Fatalf("request: model %v, max_tokens %v, max_completion_tokens %v, first message role %v; want nosuch, 3000, absent, system",
				body["model"], body["max_tokens"], body["max_completion_tokens"], first["role"])
		}
	default:
		t.Fatal("the provider received no request")
	}
}

// customModelCompletionsServer is an openai-completions endpoint that answers every request with one priced turn (1000 input and 500 output
// tokens). It returns its URL and the bodies of the requests it received.
func customModelCompletionsServer(t *testing.T) (string, <-chan map[string]any) {
	t.Helper()
	requests := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err == nil {
			select {
			case requests <- body:
			default:
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"nosuch","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"nosuch","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"nosuch","choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":500,"total_tokens":1500}}`,
			`[DONE]`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, requests
}

// customModelHomeEnv is the environment of a pig process whose home is fresh and whose agent directory holds config as its models.json.
func customModelHomeEnv(t *testing.T, config string) []string {
	t.Helper()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"HOME=" + home, "PIG_HOME=" + home, "PI_HOME=" + home, "PIG_CODING_AGENT_DIR=" + agentDir, "PI_CODING_AGENT_DIR=" + agentDir, "PIG_OFFLINE=1"}
}
