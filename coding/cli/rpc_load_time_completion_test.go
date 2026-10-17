package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A module that awaits a pi-ai completion at top level and in its factory must load and get its answers through the CLI's own Host, as under Pi, which runs
// pi-ai in the extension's process with no registration gate. This drives the production wiring (newSubprocessExtensionHost installs the early model stream);
// without it the load-time calls end with "model streaming is not available".
func TestRPCLoadTimeCompletionAnswersThroughTheCLIHost(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	dir := t.TempDir()
	fixture := filepath.Join(dir, "load-time-completion.mjs")
	if err := os.WriteFile(fixture, []byte(`import { completeSimple } from "@earendil-works/pi-ai";
const model = { id: "m", name: "m", api: "openai-completions", provider: "probe-provider", baseUrl: "`+server.URL+`/v1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
const ask = async () => { const r = await completeSimple(model, { messages: [{ role: "user", content: "ping", timestamp: 1 }] }, { apiKey: "k" }); return r.stopReason + ":" + (r.content ?? []).map((c) => c.text).join("") + (r.errorMessage ? ":" + r.errorMessage : ""); };
const atTopLevel = await ask();
export default async function (pi) {
  const inFactory = await ask();
  pi.registerCommand("load-time", { description: "report the load-time completions", handler: async (_args, ctx) => ctx.ui.notify("top=" + atTopLevel + " factory=" + inFactory, "info") });
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1"}, "--no-session", "-e", fixture)
	p.send(`{"id":"load","type":"prompt","message":"/load-time"}`)
	p.await("load-time notification", func(record rpcRecord) bool {
		if record["type"] != "extension_ui_request" || record["method"] != "notify" {
			return false
		}
		message, _ := record["message"].(string)
		if !strings.HasPrefix(message, "top=") {
			return false
		}
		if message != "top=stop:pong factory=stop:pong" {
			t.Fatalf("notification = %q, want both load-time completions answered", message)
		}
		return true
	})
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 2 || keys[0] != "Bearer k" || keys[1] != "Bearer k" {
		t.Fatalf("the server saw Authorization %q, want the module's apiKey twice", keys)
	}
}
