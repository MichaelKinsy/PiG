package coding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// storeOnlyFixture writes an agent dir whose github-copilot catalogue exists
// only in models-store.json (gemini-4-carbon, not bundled). models.json holds
// a different copilot model so the custom-provider path is populated too.
func storeOnlyFixture(t *testing.T) string {
	t.Helper()
	nowMS := time.Now().UnixMilli()
	agentDir := t.TempDir()
	carbon := `{"id":"gemini-4-carbon","name":"Gemini 4 Carbon","api":"openai-completions","provider":"github-copilot","baseUrl":"https://api.individual.githubcopilot.com","reasoning":true,"input":["text","image"],"cost":{"input":2,"output":12,"cacheRead":0.1,"cacheWrite":0},"contextWindow":1048576,"maxTokens":65536}`
	fable := `{"id":"claude-fable-5.2","name":"Claude Fable 5.2","api":"anthropic-messages","reasoning":true,"input":["text","image"],"contextWindow":1000000,"maxTokens":128000,"cost":{"input":8,"output":40,"cacheRead":0.2,"cacheWrite":10}}`
	files := map[string]string{
		"settings.json":     `{}`,
		"models.json":       `{"providers":{"github-copilot":{"models":[` + fable + `]}}}`,
		"models-store.json": fmt.Sprintf(`{"github-copilot":{"models":[%s],"checkedAt":%d,"lastModified":%d}}`, carbon, nowMS, nowMS),
		"auth.json":         fmt.Sprintf(`{"github-copilot":{"type":"oauth","refresh":"fixture","access":"fixture","expires":%d}}`, nowMS+86400000),
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(agentDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return agentDir
}

func TestBuildModelStoreOnlyModelMatchesRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", home)
	agentDir := storeOnlyFixture(t)
	svcs, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	defer svcs.Close()
	svcs.ModelRuntime().Refresh(context.Background(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})

	want := svcs.ModelRuntime().GetModel("github-copilot", "gemini-4-carbon")
	if want == nil {
		t.Fatal("ModelRuntime().GetModel(store-only) = nil; fixture is not loaded")
	}
	m, err := BuildModel("github-copilot/gemini-4-carbon", svcs)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	if m.Capabilities.ContextWindow <= 0 || m.Capabilities.ContextWindow != want.Capabilities.ContextWindow {
		t.Errorf("window = %d, want %d (>0, equal to ModelRuntime().GetModel)", m.Capabilities.ContextWindow, want.Capabilities.ContextWindow)
	}
	if levels := ai.GetSupportedThinkingLevels(m); len(levels) <= 1 {
		t.Errorf("thinking levels = %v, want more than [off]", levels)
	}
	if got := ai.ClampThinkingLevel(m, ai.ThinkingHigh); got != ai.ThinkingHigh {
		t.Errorf("clamp(high) = %q, want %q", got, ai.ThinkingHigh)
	}
	if m.ProviderMeta.API == "" {
		t.Errorf("API = empty, want non-empty")
	}
}

func TestBuildModelBundledControlKeepsCatalogValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", home)
	svcs, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: storeOnlyFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer svcs.Close()
	svcs.ModelRuntime().Refresh(context.Background(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})

	m, err := BuildModel("github-copilot/claude-opus-5.5", svcs)
	if err != nil {
		t.Fatalf("BuildModel(bundled): %v", err)
	}
	if m.Capabilities.ContextWindow <= 0 || m.ProviderMeta.API == "" || len(ai.GetSupportedThinkingLevels(m)) <= 1 {
		t.Errorf("bundled control changed: window=%d api=%q levels=%v", m.Capabilities.ContextWindow, m.ProviderMeta.API, ai.GetSupportedThinkingLevels(m))
	}
}
