package coding

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pricedModel builds a models.json model priced at 3/15/0.3/3.75 per million
// tokens whose provider answers with 1000 uncached, 1000 cached, and 500
// output tokens, and returns the usage cost Pi computes for that answer.
func pricedModel(t *testing.T) (*Services, *ai.Model, ai.UsageCost) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, `data: {"id":"c1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}`+"\n\n"+
			`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2000,"completion_tokens":500,"prompt_tokens_details":{"cached_tokens":1000}}}`+"\n\n"+
			"data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	agentDir := t.TempDir()
	models := fmt.Sprintf(`{"providers":{"priced":{"baseUrl":%q,"api":"openai-completions","authHeader":false,"models":[{"id":"model","name":"Model","cost":{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75}}]}}}`, server.URL+"/v1")
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel("priced/model", services)
	if err != nil {
		t.Fatal(err)
	}
	// Priced at run time in float64, as JavaScript evaluates calculateCost.
	price := func(rate float64, tokens int) float64 { return (rate / 1000000) * float64(tokens) }
	want := ai.UsageCost{Input: price(3, 1000), Output: price(15, 500), CacheRead: price(0.3, 1000)}
	want.Total = want.Input + want.Output + want.CacheRead + want.CacheWrite
	return services, model, want
}

// A prompt through a session prices the provider's usage with the configured
// model cost and persists it, so /session totals are not $0. Mirrors upstream
// providers calling calculateCost(model, usage) before the message is stored.
func TestSessionPersistsProviderPricedUsageCost(t *testing.T) {
	services, model, want := pricedModel(t)
	session, err := NewSession(services, SessionOptions{Model: model, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	if _, err := session.Send(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if got := persistedAssistantCost(t, session.Path()); got != want {
		t.Fatalf("persisted usage.cost = %#v, want %#v", got, want)
	}
	if stats := session.GetSessionStats(); stats.Cost != want.Total {
		t.Fatalf("session stats cost = %v, want %v", stats.Cost, want.Total)
	}
}

// The model runtime (extension complete/stream) prices with the resolved model.
func TestModelRuntimePricesProviderUsage(t *testing.T) {
	services, model, want := pricedModel(t)
	result := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
	if result.StopReason != ai.StopReasonStop || result.Usage.Cost != want {
		t.Fatalf("result stop %q usage.cost = %#v, want %#v", result.StopReason, result.Usage.Cost, want)
	}
}

func persistedAssistantCost(t *testing.T, path string) ai.UsageCost {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Role  string   `json:"role"`
				Usage ai.Usage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) == nil && entry.Type == "message" && entry.Message.Role == "assistant" {
			return entry.Message.Usage.Cost
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("no assistant message persisted in %s", path)
	return ai.UsageCost{}
}
