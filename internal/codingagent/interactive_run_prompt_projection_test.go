package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// runPromptRecordingHandle is a Session handle that records the run prompt as the Session does, so the mode treats the transcript as the owner of the request's system prompt.
type runPromptRecordingHandle struct {
	*recordingCompactHandle
	runs []*BeforeAgentStartRun
}

func (h *runPromptRecordingHandle) SetRunPrompt(run *BeforeAgentStartRun) {
	h.runs = append(h.runs, run)
}

func runPromptProjectionMode(t *testing.T, session bool) *InteractiveMode {
	t.Helper()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), Model: model, SystemPrompt: "base"})
	m.agent = mustNewAgent(agent.AgentOptions{Model: model})
	if session {
		m.opts.SessionHandle = &runPromptRecordingHandle{recordingCompactHandle: &recordingCompactHandle{agent: m.agent}}
	}
	return m
}

func requireProjection(t *testing.T, m *InteractiveMode, step string, want string, wantSet bool) {
	t.Helper()
	got, set := m.agent.SystemPromptOverride()
	if set != wantSet || got != want {
		t.Fatalf("%s: agent projects (%q, %v), want (%q, %v)", step, got, set, want, wantSet)
	}
}

// Pi projects a prompt onto the request only when before_agent_start returned systemPrompt (agent-session.ts:1792-1807); the prompt of every other run reaches the provider as the transcript's sections and patch messages (1743-1755). A run no handler forced must leave the agent without a projection, which would collapse a mid-conversation patch into the head.
func TestInteractiveRunPromptProjectsOnlyAForcedPrompt(t *testing.T) {
	m := runPromptProjectionMode(t, true)

	if err := m.beginRunPrompt(1, BeforeAgentStartRun{}); err != nil {
		t.Fatal(err)
	}
	requireProjection(t, m, "run no handler touched", "", false)
	m.refreshForcedPrompt()
	requireProjection(t, m, "tool change during that run", "", false)
	m.endRunPrompt(1)
	requireProjection(t, m, "end of that run", "", false)
	m.refreshForcedPrompt()
	requireProjection(t, m, "tool change between runs", "", false)

	forced := "forced by a handler"
	if err := m.beginRunPrompt(2, BeforeAgentStartRun{SystemPrompt: &forced}); err != nil {
		t.Fatal(err)
	}
	requireProjection(t, m, "forced run", forced, true)
	m.refreshForcedPrompt()
	requireProjection(t, m, "tool change during the forced run", forced, true)
	m.endRunPrompt(2)
	requireProjection(t, m, "end of the forced run", "", false)
}

// A mode without a Session has no transcript to record the prompt in, so its agent carries the prompt of every run.
func TestInteractiveRunPromptProjectsEveryPromptWithoutASession(t *testing.T) {
	m := runPromptProjectionMode(t, false)
	if err := m.beginRunPrompt(1, BeforeAgentStartRun{}); err != nil {
		t.Fatal(err)
	}
	requireProjection(t, m, "run no handler touched", "base", true)
	m.endRunPrompt(1)
	requireProjection(t, m, "end of the run", "base", true)
}
