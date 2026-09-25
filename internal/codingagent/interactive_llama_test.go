package codingagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/tui"
)

var llamaTestANSI = regexp.MustCompile(`\x1b\[[0-9;]*m|\x1b\]8;;[^\x07]*\x07|\x1b_[^\x07]*\x07`)

// llamaRouter is a llama.cpp router whose loads complete immediately.
type llamaRouter struct {
	mu     sync.Mutex
	status map[string]string
}

func (r *llamaRouter) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch request.URL.Path {
	case "/models/load":
		var body struct{ Model string }
		_ = json.NewDecoder(request.Body).Decode(&body)
		r.status[body.Model] = "loaded"
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	case "/models":
		data := []any{}
		for _, id := range []string{"alpha", "beta"} {
			data = append(data, map[string]any{"id": id, "status": map[string]any{"value": r.status[id]}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	case "/props":
		_ = json.NewEncoder(w).Encode(map[string]any{})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newLlamaTestMode(t *testing.T) (*InteractiveMode, *ai.AuthStorage, string, string) {
	t.Helper()
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("LLAMA_API_KEY", "")
	server := httptest.NewServer(&llamaRouter{status: map[string]string{"alpha": "loaded", "beta": "unloaded"}})
	t.Cleanup(server.Close)
	dir := t.TempDir()
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	registry.SetAuthStorage(auth)
	host := llama.NewHost(registry, auth, ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")))
	m := &InteractiveMode{
		tuiInst:         tui.NewWithOutput(io.Discard, 120, 40),
		editorContainer: tui.NewContainer(),
		editor:          tui.NewEditor(),
		chatContainer:   tui.NewContainer(),
		opts:            InteractiveOptions{Llama: host, AgentDir: dir, ModelRegistry: registry},
	}
	return m, auth, server.URL, dir
}

func plainRender(component tui.Component) string {
	return llamaTestANSI.ReplaceAllString(strings.Join(component.Render(400), "\n"), "")
}

func waitForRender(t *testing.T, component tui.Component, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(plainRender(component), want) {
		if time.Now().After(deadline) {
			t.Fatalf("never rendered %q; last render:\n%s", want, plainRender(component))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLlamaCommandRunsTheManagerInTheEditorSlot(t *testing.T) {
	m, auth, url, _ := newLlamaTestMode(t)
	if err := auth.Set(llama.LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Env: map[string]string{"LLAMA_BASE_URL": url}}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- m.runLlamaCommand(context.Background()) }()

	waitForRender(t, m.editorContainer, "beta")
	deliverModalInput(t, m, []byte("\x1b[B"))
	deliverModalInput(t, m, []byte("\r"))
	waitForRender(t, m.editorContainer, "1 model is loaded")
	deliverModalInput(t, m, []byte("\x1b[B"))
	deliverModalInput(t, m, []byte("\r"))
	waitForRender(t, m.chatContainer, "Loaded beta")
	waitForRender(t, m.editorContainer, "load/unload/download")
	deliverModalInput(t, m, []byte("\x1b"))
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("/llama did not return after the manager closed")
	}
	if text := plainRender(m.editorContainer); strings.Contains(text, "llama.cpp models") {
		t.Fatalf("manager view still shown:\n%s", text)
	}
	if entry, ok := m.opts.ModelRegistry.Resolve(llama.LlamaProviderID, "beta"); !ok || entry.BaseURL != url+"/v1" {
		t.Fatalf("beta after /llama = %+v, %v", entry, ok)
	}
}

func TestLlamaCommandWarnsWhenNotConfigured(t *testing.T) {
	m, _, _, _ := newLlamaTestMode(t)
	if err := m.runLlamaCommand(context.Background()); err != nil {
		t.Fatal(err)
	}
	if text := plainRender(m.chatContainer); !strings.Contains(text, "Configure llama.cpp with /login llama.cpp") {
		t.Fatalf("chat = %q", text)
	}
}

func TestLlamaLoginStoresServerAndKeyThenGuidesModelSelection(t *testing.T) {
	m, auth, url, dir := newLlamaTestMode(t)
	handled := make(chan bool, 1)
	go func() { handled <- m.loginAPIKeyProvider(llama.LlamaProviderID) }()
	waitForRender(t, m.editorContainer, "llama.cpp server URL")
	deliverModalInput(t, m, []byte(url))
	deliverModalInput(t, m, []byte("\r"))
	waitForRender(t, m.editorContainer, "API key (optional)")
	deliverModalInput(t, m, []byte("secret"))
	deliverModalInput(t, m, []byte("\r"))
	select {
	case ok := <-handled:
		if !ok {
			t.Fatal("llama.cpp login was not handled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("llama.cpp login did not finish")
	}
	credential, ok, err := auth.Get(llama.LlamaProviderID)
	if err != nil || !ok || credential.Key != "secret" || credential.Env["LLAMA_BASE_URL"] != url {
		t.Fatalf("stored credential = %+v, %v, %v", credential, ok, err)
	}
	chat := plainRender(m.chatContainer)
	for _, want := range []string{
		"Saved API key for llama.cpp. Credentials saved to " + filepath.Join(dir, "auth.json"),
		"Saved API key for llama.cpp. No llama.cpp models are loaded. Use /llama to load a model, then /model to select it.",
	} {
		if !strings.Contains(chat, want) {
			t.Fatalf("chat is missing %q:\n%s", want, chat)
		}
	}
	// The background catalog refresh persists the catalog; wait for it so the
	// temporary agent directory is idle before cleanup.
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(dir, "models-store.json"))
		if strings.Contains(string(data), `"llama.cpp"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("catalog refresh never persisted: %s", data)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLlamaLoginCancelShowsNoError(t *testing.T) {
	m, auth, _, _ := newLlamaTestMode(t)
	handled := make(chan bool, 1)
	go func() { handled <- m.loginAPIKeyProvider(llama.LlamaProviderID) }()
	waitForRender(t, m.editorContainer, "llama.cpp server URL")
	deliverModalInput(t, m, []byte("\x1b"))
	if ok := <-handled; !ok {
		t.Fatal("cancelled login was not handled")
	}
	if _, stored, _ := auth.Get(llama.LlamaProviderID); stored {
		t.Fatal("cancelled login stored a credential")
	}
	if chat := plainRender(m.chatContainer); strings.Contains(chat, "Failed") {
		t.Fatalf("cancelled login reported an error:\n%s", chat)
	}
	if m.loginAPIKeyProvider("openai") {
		t.Fatal("plain api-key providers must keep the key prompt")
	}
}

func TestLlamaAppearsInTheAPIKeyLoginList(t *testing.T) {
	m, _, url, _ := newLlamaTestMode(t)
	t.Setenv("LLAMA_BASE_URL", url)
	providers := m.oauthProviderList("login-api-key")
	index := slices.IndexFunc(providers, func(p tui.OAuthProvider) bool { return p.ID == llama.LlamaProviderID })
	if index <= 0 || providers[index-1].Name != "Kimi For Coding" {
		t.Fatalf("llama.cpp position %d in %+v", index, providers)
	}
	entry := providers[index]
	if entry.Name != "llama.cpp" || entry.AuthType != "api_key" || entry.AuthStatusSource != "environment" || entry.AuthStatusLabel != "LLAMA_BASE_URL" {
		t.Fatalf("llama.cpp entry = %+v", entry)
	}
}

func TestLlamaSlashHandlerAndLoginRouting(t *testing.T) {
	var appended []string
	sc := &SlashContext{Append: func(text string) { appended = append(appended, text) }}
	if err := llamaHandler(sc); err != nil || len(appended) != 1 {
		t.Fatalf("llamaHandler without a host = %v, %v", err, appended)
	}
	called := false
	sc.RunLlama = func() error { called = true; return nil }
	if err := llamaHandler(sc); err != nil || !called {
		t.Fatalf("llamaHandler = %v, called %v", err, called)
	}

	var routed string
	textInput := false
	login := &SlashContext{
		ShowLoginAuthType:   func() (string, bool) { return "api_key", true },
		ShowOAuthSelector:   func(string) (string, bool) { return llama.LlamaProviderID, true },
		SetAPIKey:           func(string, string) error { return nil },
		ShowTextInput:       func(string, string) (string, bool) { textInput = true; return "", false },
		LoginAPIKeyProvider: func(provider string) bool { routed = provider; return true },
	}
	if err := loginHandler(login); err != nil || routed != llama.LlamaProviderID || textInput {
		t.Fatalf("loginHandler routed %q, text input %v, err %v", routed, textInput, err)
	}
}
