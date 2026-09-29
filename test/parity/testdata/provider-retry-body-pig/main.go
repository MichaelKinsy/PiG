package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "277403")
		w.WriteHeader(429)
		_, _ = fmt.Fprint(w, "rate limited")
	}))
	defer server.Close()
	dir, err := os.MkdirTemp("", "pig-retry-body-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	config := fmt.Sprintf(`{"providers":{"opencode-go":{"api":"openai-completions","baseUrl":%q,"models":[{"id":"test-model","name":"Test Model","reasoning":false,"input":["text"],"contextWindow":1000,"maxTokens":100}]}}}`, server.URL)
	if err = os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
		return err
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		return err
	}
	model, err := coding.BuildModel("opencode-go/test-model", services)
	if err != nil {
		return err
	}
	if err = ai.ConfigureProviderRetry(2, 1000); err != nil {
		return err
	}
	result := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}}}, Tools: []ai.ToolSchema{}}, ai.StreamOptions{APIKey: "test"})
	return json.NewEncoder(os.Stdout).Encode(struct {
		StopReason ai.StopReason `json:"stopReason"`
		Limit      bool          `json:"limit"`
		Detail     bool          `json:"detail"`
		Attempts   int32         `json:"attempts"`
	}{result.StopReason, strings.Contains(result.ErrorMessage, "Server requested 277403s retry delay (max: 1s)"), strings.Contains(result.ErrorMessage, "rate limited"), attempts.Load()})
}
