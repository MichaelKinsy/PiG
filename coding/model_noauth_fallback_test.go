package coding

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// noAuthRowTimeout bounds one Stream so a recursive or endless fallback fails the row instead of hanging the run.
const noAuthRowTimeout = 20 * time.Second

// noAuthProvider is a builtin provider without a native registration. Its store-only
// model (gemini-4-carbon) is not bundled, so buildModel's store-only branch resolves it.
const noAuthProvider = "github-copilot"

// noAuthFixture writes an agent dir whose noAuthProvider catalogue exists only in
// models-store.json, with baseUrl pointed at baseURL. There is no auth.json and no
// models.json entry, so the provider has no sign-in and no key. Env names are cleared.
func noAuthFixture(t *testing.T, baseURL string) string {
	t.Helper()
	for _, name := range []string{"GITHUB_TOKEN", "COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_COPILOT_API_KEY"} {
		t.Setenv(name, "")
	}
	nowMS := time.Now().UnixMilli()
	agentDir := t.TempDir()
	carbon := fmt.Sprintf(`{"id":"gemini-4-carbon","name":"Gemini 4 Carbon","api":"openai-completions","provider":"%s","baseUrl":%q,"reasoning":true,"input":["text","image"],"cost":{"input":2,"output":12,"cacheRead":0.1,"cacheWrite":0},"contextWindow":1048576,"maxTokens":65536}`, noAuthProvider, baseURL)
	files := map[string]string{
		"settings.json":     `{}`,
		"models-store.json": fmt.Sprintf(`{"%s":{"models":[%s],"checkedAt":%d,"lastModified":%d}}`, noAuthProvider, carbon, nowMS, nowMS),
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(agentDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return agentDir
}

// noAuthRuntime opens services over the fixture and loads the store with no network.
func noAuthRuntime(t *testing.T, agentDir string) *AgentSessionServices {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", home)
	svcs, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svcs.Close() })
	svcs.ModelRuntime().Refresh(context.Background(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	return svcs
}

// noAuthServer is a loopback server that answers every request with a 500 and records each Authorization header.
type noAuthServer struct {
	*httptest.Server
	mu    sync.Mutex
	auths []string
}

func newNoAuthServer(t *testing.T) *noAuthServer {
	t.Helper()
	s := &noAuthServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		s.mu.Unlock()
		http.Error(w, `{"error":"fixture"}`, http.StatusInternalServerError)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *noAuthServer) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auths...)
}

// streamWithDeadline runs one turn through the runtime and fails the row (not the run) if it does not end within noAuthRowTimeout.
func streamWithDeadline(t *testing.T, svcs *AgentSessionServices, model *ai.Model, options ai.StreamOptions) *ai.AssistantMessage {
	t.Helper()
	done := make(chan *ai.AssistantMessage, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), noAuthRowTimeout)
		defer cancel()
		request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}
		done <- svcs.ModelRuntime().Stream(ctx, model, request, options).Result()
	}()
	select {
	case msg := <-done:
		return msg
	case <-time.After(noAuthRowTimeout + 5*time.Second):
		t.Fatalf("recursion/hang: Stream did not end within %s", noAuthRowTimeout)
		return nil
	}
}

// Row A: no sign-in, no key. services.go:654 refuses before buildModel, so the turn must end in an auth-missing error and no request may reach the server.
func TestNoAuthRowA_NoSignInNoKeyFailsWithoutRecursion(t *testing.T) {
	server := newNoAuthServer(t)
	svcs := noAuthRuntime(t, noAuthFixture(t, server.URL))
	model := svcs.ModelRuntime().GetModel(noAuthProvider, "gemini-4-carbon")
	if model == nil {
		t.Fatal("store-only model not loaded; fixture is wrong")
	}
	msg := streamWithDeadline(t, svcs, model, ai.StreamOptions{})
	if msg == nil || msg.StopReason != ai.StopReasonError {
		t.Fatalf("row A: message = %+v, want an error stop", msg)
	}
	t.Logf("row A: stop=%q error=%q", msg.StopReason, msg.ErrorMessage)
	if n := len(server.received()); n != 0 {
		t.Errorf("row A: server received %d request(s), want 0", n)
	}
}

// Row B: no registry sign-in, explicit request key. The key must reach the server, as on base.
func TestNoAuthRowB_ExplicitKeyReachesServer(t *testing.T) {
	server := newNoAuthServer(t)
	svcs := noAuthRuntime(t, noAuthFixture(t, server.URL))
	model := svcs.ModelRuntime().GetModel(noAuthProvider, "gemini-4-carbon")
	if model == nil {
		t.Fatal("store-only model not loaded; fixture is wrong")
	}
	msg := streamWithDeadline(t, svcs, model, ai.StreamOptions{APIKey: "fixture-key"})
	if msg != nil {
		t.Logf("row B: stop=%q error=%q", msg.StopReason, msg.ErrorMessage)
	}
	got := server.received()
	if len(got) == 0 {
		t.Fatalf("row B: server received no request; want one carrying the explicit key")
	}
	if got[0] != "Bearer fixture-key" {
		t.Errorf("row B: Authorization = %q, want %q", got[0], "Bearer fixture-key")
	}
}

// Control: a bundled model through the same no-sign-in, no-key path keeps its error outcome and sends nothing.
func TestNoAuthControlBundledModelUnchanged(t *testing.T) {
	server := newNoAuthServer(t)
	svcs := noAuthRuntime(t, noAuthFixture(t, server.URL))
	model := svcs.ModelRuntime().GetModel(noAuthProvider, "claude-opus-5.5")
	if model == nil {
		t.Fatal("bundled control model not loaded")
	}
	msg := streamWithDeadline(t, svcs, model, ai.StreamOptions{})
	if msg == nil || msg.StopReason != ai.StopReasonError {
		t.Fatalf("control: message = %+v, want an error stop", msg)
	}
	t.Logf("control: stop=%q error=%q", msg.StopReason, msg.ErrorMessage)
	if n := len(server.received()); n != 0 {
		t.Errorf("control: server received %d request(s), want 0", n)
	}
}
