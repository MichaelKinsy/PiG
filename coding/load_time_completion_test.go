package coding

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pi runs pi-ai in the extension's own process, so a module may await a completion at top level or in its factory in any host, an SDK session included. A Host with
// no UI bridge answered no call it had no handler for, and the Node module waited forever for the model stream. loadTimeCompletionModule writes a module that
// records what both of its load-time completions answered.
func loadTimeCompletionModule(t *testing.T) (module, answers string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	module, answers = filepath.Join(dir, "load-time-completion.mjs"), filepath.Join(dir, "answers.txt")
	source := `import { writeFileSync } from "node:fs";
import { completeSimple } from "@earendil-works/pi-ai";
const model = { id: "m", name: "m", api: "openai-completions", provider: "probe-provider", baseUrl: "` + server.URL + `/v1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
const ask = async () => { const r = await completeSimple(model, { messages: [{ role: "user", content: "ping", timestamp: 1 }] }, { apiKey: "k" }); return r.stopReason + ":" + (r.content ?? []).map((c) => c.text).join("") + (r.errorMessage ? "(" + r.errorMessage + ")" : ""); };
const atTopLevel = await ask();
export default async function (pi) {
  const inFactory = await ask();
  writeFileSync(` + fmt.Sprintf("%q", answers) + `, "top=" + atTopLevel + " factory=" + inFactory);
  pi.registerCommand("load-time", { description: "load-time completions", handler: async () => {} });
}
`
	if err := os.WriteFile(module, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return module, answers
}

func requireLoadTimeAnswers(t *testing.T, answers string) {
	t.Helper()
	data, err := os.ReadFile(answers)
	if err != nil {
		t.Fatalf("the module recorded no answers: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "top=stop:pong factory=stop:pong" {
		t.Fatalf("load-time completions answered %q, want both answered by the model", got)
	}
}

func TestDiscoverAndLoadExtensionsAnswersLoadTimeCompletions(t *testing.T) {
	t.Parallel()
	module, answers := loadTimeCompletionModule(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	result, err := DiscoverAndLoadExtensions(ctx, []string{module}, cwd, agentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if len(result.Errors) != 0 || len(result.Extensions) != 1 {
		t.Fatalf("errors = %v, extensions = %d, want the module loaded", result.Errors, len(result.Extensions))
	}
	requireLoadTimeAnswers(t, answers)
}

func TestDefaultResourceLoaderAnswersLoadTimeCompletions(t *testing.T) {
	t.Parallel()
	module, answers := loadTimeCompletionModule(t)
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir, NoExtensions: true, AdditionalExtensionPaths: []string{module}})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := loader.ReloadWith(ctx); err != nil {
		t.Fatal(err)
	}
	defer loader.Close()
	requireLoadTimeAnswers(t, answers)
}
