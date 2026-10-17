package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/footer.ts

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestFooterWidthUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/footer-width.test.ts:105
	t.Run("does not abbreviate sibling paths that share the home prefix", func(t *testing.T) {
		if got := formatCwdForFooter("/home/user2", "/home/user"); got != "/home/user2" {
			t.Fatalf("cwd = %q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/footer-width.test.ts:109
	t.Run("abbreviates the home directory and descendants", func(t *testing.T) {
		if got := formatCwdForFooter("/home/user", "/home/user"); got != "~" {
			t.Fatalf("home = %q", got)
		}
		if got := formatCwdForFooter("/home/user/project", "/home/user"); got != "~"+string(filepath.Separator)+"project" {
			t.Fatalf("descendant = %q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/footer-width.test.ts:120
	t.Run("keeps all lines within width for wide session names", func(t *testing.T) {
		footer := upstreamFooter(t, nil, "test", "test-model")
		footer.SetName(strings.Repeat("한글", 30))
		assertUpstreamFooterWidth(t, footer, 93)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/footer-width.test.ts:131
	t.Run("keeps stats line within width for wide model and provider names", func(t *testing.T) {
		usage := &ai.Usage{Input: 12345, Output: 6789, Cost: ai.UsageCost{Total: 1.234}}
		footer := upstreamFooter(t, usage, "공급자", strings.Repeat("模", 30))
		footer.model.Capabilities.MaxThinking = ai.ModelThinkingLevel("high").ReasoningOption()
		footer.SetThinkingLevel("high")
		footer.SetProviderCount(2)
		assertUpstreamFooterWidth(t, footer, 60)
	})
	// .upstream/v0.99.1/packages/coding-agent/test/footer-width.test.ts:160
	t.Run("shows the physical model a virtual model routed to", func(t *testing.T) {
		footer := upstreamFooter(t, nil, "test", "auto")
		footer.model.Capabilities.MaxThinking = ai.ModelThinkingLevel("high").ReasoningOption()
		footer.SetThinkingLevel("high")
		footer.SetSession(testFooterSession{routed: func() *RoutedModelSelection {
			return &RoutedModelSelection{Model: footerTestModel("test", "gpt-5.6-luna", 0, 0), ThinkingLevel: ai.ModelThinkingLevel("medium")}
		}})
		assertUpstreamFooterStats(t, footer, "auto \u2022 high \u2192 gpt-5.6-luna \u2022 medium")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/footer-width.test.ts:155
	t.Run("includes summary and tool result usage in the total cost", func(t *testing.T) {
		session := NewSession("footer", "/tmp/project")
		if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Usage: &ai.Usage{Input: 100, Output: 10, Cost: ai.UsageCost{Total: 0.5}}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := session.BranchWithSummary(nil, "", nil, false, &ai.Usage{Input: 20, Output: 5, Cost: ai.UsageCost{Total: 0.25}}); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendCompaction("", "", 0, nil, false, &ai.Usage{Input: 5, Output: 2, Cost: ai.UsageCost{Total: 0.125}}); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, Usage: &ai.Usage{Input: 15, Output: 3, Cost: ai.UsageCost{Total: 0.375}}}}); err != nil {
			t.Fatal(err)
		}
		footer := upstreamFooter(t, nil, "test", "test-model")
		footer.SetSession(testFooterSession{totals: session.FooterUsageTotals})
		assertUpstreamFooterStats(t, footer, "$1.250")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/footer-width.test.ts:213
	t.Run("updates cached usage totals after an entry is appended", func(t *testing.T) {
		usage := &ai.Usage{Input: 10, Output: 1, Cost: ai.UsageCost{Total: 0.5}}
		session := NewSession("footer", "/tmp/project")
		if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Usage: usage}}); err != nil {
			t.Fatal(err)
		}
		m := &InteractiveMode{opts: InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: session}, Model: footerTestModel("test", "test-model", 0, 0), AgentDir: t.TempDir()}}
		footer := m.newFooter()
		footer.SetModel(m.opts.Model)
		footer.cwd, footer.gitBranch = "/tmp/project", "main"
		footer.SetProviderCount(1)
		assertUpstreamFooterStats(t, footer, "$0.500")

		if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Usage: usage}}); err != nil {
			t.Fatal(err)
		}
		assertUpstreamFooterStats(t, footer, "$1.000")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/footer-width.test.ts:193
	t.Run("shows the latest cache hit rate when cache usage is present", func(t *testing.T) {
		footer := upstreamFooter(t, &ai.Usage{Input: 100, Output: 10, CacheRead: 50, CacheWrite: 50, Cost: ai.UsageCost{Total: 0.001}}, "test", "test-model")
		assertUpstreamFooterStats(t, footer, "CH25.0%")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/footer-width.test.ts:210
	t.Run("marks Kimi Coding costs as subscription estimates", func(t *testing.T) {
		footer := upstreamFooter(t, &ai.Usage{Input: 100, Output: 10, Cost: ai.UsageCost{Total: 1.234}}, "kimi-coding", "test-model")
		assertUpstreamFooterStats(t, footer, "$1.234 (sub)")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/footer-width.test.ts:227
	t.Run("marks explicitly identified subscription auth", func(t *testing.T) {
		footer := upstreamFooter(t, nil, "anthropic", "test-model")
		footer.SetUsingSubscription(true)
		assertUpstreamFooterStats(t, footer, "$0.000 (sub)")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/footer-width.test.ts:234
	t.Run("does not mark generic OAuth sign-in as a subscription", func(t *testing.T) {
		footer := upstreamFooter(t, &ai.Usage{Input: 100, Output: 10, Cost: ai.UsageCost{Total: 1.234}}, "openrouter", "test-model")
		assertUpstreamFooterStats(t, footer, "$1.234")
		if got := stripANSI(footer.Render(120)[1]); strings.Contains(got, "(sub)") {
			t.Fatalf("unexpected subscription: %q", got)
		}
	})
}

func upstreamFooter(t *testing.T, usage *ai.Usage, provider, model string) *FooterComponent {
	t.Helper()
	session := NewSession("footer", "/tmp/project")
	if usage != nil {
		if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Usage: usage}}); err != nil {
			t.Fatal(err)
		}
	}
	m := &InteractiveMode{opts: InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: session}, Model: footerTestModel(provider, model, 0, 0), AgentDir: t.TempDir()}}
	footer := m.newFooter()
	footer.SetModel(m.opts.Model)
	footer.cwd, footer.gitBranch = "/tmp/project", "main"
	footer.SetProviderCount(1)
	footer.contextTokens = 24600 // Upstream getContextUsage returns 12.3% of 200000.
	return footer
}

func assertUpstreamFooterWidth(t *testing.T, footer *FooterComponent, width int) {
	t.Helper()
	for _, line := range footer.Render(width) {
		if got := widthx.VisibleWidth(line); got > width {
			t.Fatalf("line width = %d, want <= %d: %q", got, width, line)
		}
	}
}

func assertUpstreamFooterStats(t *testing.T, footer *FooterComponent, want string) {
	t.Helper()
	if got := stripANSI(footer.Render(120)[1]); !strings.Contains(got, want) {
		t.Fatalf("stats = %q, want %q", got, want)
	}
}
