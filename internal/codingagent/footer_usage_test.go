package codingagent

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type footerTestProvider struct {
	ai.Provider
	id string
}

func (provider footerTestProvider) ID() string { return provider.id }

func footerTestModel(providerID, modelID string, inputRate, outputRate float64) *ai.Model {
	return &ai.Model{
		ID:       modelID,
		Provider: footerTestProvider{id: providerID},
		Capabilities: ai.ModelCapabilities{
			ContextWindow: 200_000, InputCostPer1M: inputRate, OutputCostPer1M: outputRate,
		},
		ProviderMeta: ai.ProviderMetadata{ProviderID: providerID},
	}
}

// writePricedSession persists a session whose assistant and tool-result
// entries carry stored costs, and returns the reloaded (resumed) session.
func writePricedSession(t *testing.T, assistantCost, toolCost float64) *Session {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	session := NewSession("footer-cost", t.TempDir())
	session.SetPath(path)
	if _, err := session.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: agent.RoleAssistant, Provider: "github-copilot", ModelID: "gpt-5.5",
		Content:    []ai.AssistantContentBlock{ai.ToolCall{ID: "call", Name: "read"}},
		StopReason: ai.StopReasonToolUse,
		Usage:      &ai.Usage{Input: 1000, Output: 200, CacheRead: 3000, Cost: ai.UsageCost{Total: assistantCost}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: agent.RoleToolResult, ToolCallID: "call", ToolName: "read",
		Usage: &ai.Usage{Input: 10, Output: 5, Cost: ai.UsageCost{Total: toolCost}},
	}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func resumedFooterLine(t *testing.T, session *Session, active *ai.Model, agentDir string) string {
	t.Helper()
	m := &InteractiveMode{
		opts:          InteractiveOptions{SessionHandle: &recordingCompactHandle{inner: session}, Model: active, AgentDir: agentDir},
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(&bytes.Buffer{}, 160, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
	}
	m.statusLine = m.newFooter()
	m.renderSessionEntries()
	m.updateProviderInfo()
	return stripANSI(m.statusLine.Render(160)[1])
}

// Upstream footer.ts sums each entry's stored usage.cost.total. A session
// priced under one model and resumed under another keeps its stored cost.
func TestResumedPricedSessionFooterShowsStoredCost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	session := writePricedSession(t, 0.123, 0.05)
	// Re-pricing these tokens at the active model's rates would give $0.030.
	active := footerTestModel("anthropic", "claude-opus-4-8", 15, 75)
	line := resumedFooterLine(t, session, active, t.TempDir())
	if !strings.Contains(line, "$0.173") || strings.Contains(line, "(sub)") {
		t.Fatalf("footer = %q, want the stored $0.173 without (sub)", line)
	}
	if !strings.Contains(line, "↑1.0k") || !strings.Contains(line, "↓205") || !strings.Contains(line, "R3.0k") {
		t.Fatalf("footer = %q, want summed stored tokens", line)
	}
	if !strings.Contains(line, "CH75.0%") {
		t.Fatalf("footer = %q, want the latest assistant cache-hit rate", line)
	}
}

// A subscription session stores zero cost. Pi shows "$0.000 (sub)" for it;
// PiG used to re-price the tokens at the active model's rates.
func TestResumedSubscriptionSessionFooterShowsZeroStoredCost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	session := writePricedSession(t, 0, 0)
	active := footerTestModel("kimi-coding", "kimi-for-coding", 60, 300)
	line := resumedFooterLine(t, session, active, t.TempDir())
	if !strings.Contains(line, "$0.000 (sub)") {
		t.Fatalf("footer = %q, want $0.000 (sub)", line)
	}
}

func TestFooterUsingSubscriptionFollowsPiRules(t *testing.T) {
	agentDir := t.TempDir()
	auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"anthropic", "openrouter"} {
		if err := auth.Set(provider, ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := auth.Set("openai-codex", ai.Credential{Type: ai.CredentialAPIKey, Key: "key"}); err != nil {
		t.Fatal(err)
	}
	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: agentDir}}
	for _, tc := range []struct {
		provider string
		want     bool
	}{
		{"anthropic", true},     // subscription OAuth login
		{"openrouter", false},   // OAuth, but not a subscription login
		{"openai-codex", false}, // subscription provider, API-key auth
		{"kimi-coding", true},   // subscription-backed despite API-key auth
		{"github-copilot", false},
	} {
		if got := m.footerUsingSubscription(footerTestModel(tc.provider, "model", 1, 1)); got != tc.want {
			t.Errorf("%s: usingSubscription = %v, want %v", tc.provider, got, tc.want)
		}
	}

	// The marker follows the active model when it changes.
	footer := m.newFooter()
	footer.SetModel(footerTestModel("anthropic", "claude-opus-4-8", 1, 1))
	if !footer.usingSubscription {
		t.Fatal("switching to a subscription model did not set (sub)")
	}
	footer.SetModel(footerTestModel("openrouter", "model", 1, 1))
	if footer.usingSubscription {
		t.Fatal("switching to a non-subscription model kept (sub)")
	}
}

// footer.ts formats with Number.prototype.toFixed, which rounds an exact tie
// up; Go's "%.3f" would print $0.062 and CH12.2%.
func TestFooterUsagePartsUseJavaScriptRounding(t *testing.T) {
	rate := 12.25
	parts := strings.Join(footerUsageParts(footerUsageTotals{input: 5, cacheRead: 1, cost: 0.0625, latestCacheHitRate: &rate}, false), " ")
	if parts != "↑5 R1 CH12.3% $0.063" {
		t.Fatalf("parts = %q", parts)
	}
	if parts := footerUsageParts(footerUsageTotals{input: 5}, false); len(parts) != 1 {
		t.Fatalf("zero cost without a subscription must hide the cost: %q", parts)
	}
}

// /session prints the stored-cost totals with toFixed(3), like Pi.
func TestSessionCommandPrintsStoredCostWithJavaScriptRounding(t *testing.T) {
	session := writePricedSession(t, 0.0625, 0)
	var output string
	sc := &SlashContext{
		CurrentSession: func() *Session { return session },
		AppendText:     func(text string) { output = text },
	}
	if err := sessionHandler(sc); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(output)
	if !strings.Contains(plain, "Total: $0.063") || !strings.Contains(plain, "github-copilot/gpt-5.5: $0.063") {
		t.Fatalf("/session output = %q", plain)
	}
}
