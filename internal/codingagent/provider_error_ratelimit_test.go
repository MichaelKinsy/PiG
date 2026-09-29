package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi interactive-mode.ts message_end leaves provider diagnostics to the assistant component. It does not classify providers or add a second auth/expiry message.
func TestProviderErrorDisplayPreservesDiagnosticsWithoutDuplicateStatus(t *testing.T) {
	for _, raw := range []string{
		`github-copilot: token refresh failed: credentials may have expired or the network is unavailable: HTTP 403: {"message":"API rate limit exceeded"}`,
		`github-copilot: HTTP 401: {"message":"Bad credentials"}`,
		`anthropic: HTTP 401: {"message":"invalid x-api-key"}`,
		"openai: HTTP 400:\n{\n  bad request\n}",
		"custom endpoint rejected request",
	} {
		t.Run(raw, func(t *testing.T) {
			m := resumeThinkingMode(t, false)
			prepareAssistantEventTest(t, m)
			m.statusLine.SetStatusHook(m.showStatus)
			msg := assistantMsg("")
			msg.Assistant.StopReason = ai.StopReasonError
			msg.Assistant.ErrorMessage = raw
			m.handleAgentEvent(agent.MessageStartEvent{Message: msg})
			m.handleAgentEvent(agent.MessageEndEvent{Message: msg})
			lines := stripANSITest(strings.Join(m.chatContainer.Render(240), "\n"))
			for part := range strings.SplitSeq(raw, "\n") {
				if !strings.Contains(lines, part) {
					t.Errorf("lost provider diagnostic %q: %q", part, lines)
				}
			}
			for _, unexpected := range []string{"Provider request failed", "auth expired", "GitHub Copilot authentication failed"} {
				if strings.Contains(lines, unexpected) {
					t.Errorf("invented status %q: %q", unexpected, lines)
				}
			}
		})
	}
}
