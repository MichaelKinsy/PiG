package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi runs pi-ai in the extension's own process, so a module may await a completion at top level or in its factory in whichever host loads it. A Host with no
// handler for the model stream answered nothing and the Node module waited forever (B1). Each loader that builds its own Host answers it: the extension
// validation, the pre-session authentication inspection (by configuration and by name) and the login preview.
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

func TestExtensionValidationAnswersLoadTimeCompletions(t *testing.T) {
	module, answers := loadTimeCompletionModule(t)
	t.Setenv("PIG_HOME", t.TempDir())
	reports, err := validateExtensionRuntimes(module)
	if err != nil || len(reports) != 1 || !reports[0].Valid {
		t.Fatalf("validation = %+v, %v, want the module valid", reports, err)
	}
	requireLoadTimeAnswers(t, answers)
}

func TestAuthContributionInspectionAnswersLoadTimeCompletions(t *testing.T) {
	module, answers := loadTimeCompletionModule(t)
	t.Setenv("PIG_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	registry := &authContributionRegistry{cwd: t.TempDir(), ctx: ctx, targets: map[string]authContribution{}, owners: map[string]string{}, configs: map[string]subprocess.ExtConfig{}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}}
	defer registry.close()
	if err := registry.inspectConfigs([]subprocess.ExtConfig{{Name: "load-time", Source: module, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if len(registry.diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v, want the module inspected", registry.diagnostics)
	}
	requireLoadTimeAnswers(t, answers)
}

func TestAuthContributionLoadByNameAnswersLoadTimeCompletions(t *testing.T) {
	module, answers := loadTimeCompletionModule(t)
	t.Setenv("PIG_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	registry := &authContributionRegistry{cwd: t.TempDir(), ctx: ctx, targets: map[string]authContribution{}, configs: map[string]subprocess.ExtConfig{"load-time": {Name: "load-time", Source: module, Enabled: true}}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}}
	defer registry.close()
	if err := registry.loadExtension("load-time"); err != nil {
		t.Fatal(err)
	}
	requireLoadTimeAnswers(t, answers)
}

func TestExtensionLoginPreviewAnswersLoadTimeCompletions(t *testing.T) {
	module, answers := loadTimeCompletionModule(t)
	t.Setenv("PIG_HOME", t.TempDir())
	// The preview has nothing to show for a module that registers no login; the load, and the answers the module recorded while it loaded, are what this checks.
	_ = runExtensionLoginPreview([]string{module})
	requireLoadTimeAnswers(t, answers)
}
