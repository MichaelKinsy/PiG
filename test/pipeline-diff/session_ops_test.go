package pipelinediff

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// sessionOp is one operation on a session positioned at an entry of a tree.
type sessionOp struct {
	name  string
	apply func(t *testing.T, s *coding.Session, p *recordingProvider, entryID string) (ran bool)
}

var sessionOps = []sessionOp{
	{"navigate", func(t *testing.T, s *coding.Session, _ *recordingProvider, id string) bool {
		_, err := s.NavigateTree(t.Context(), id, coding.NavigateTreeOptions{})
		return err == nil
	}},
	{"navigate+summary", func(t *testing.T, s *coding.Session, _ *recordingProvider, id string) bool {
		_, err := s.NavigateTree(t.Context(), id, coding.NavigateTreeOptions{Summarize: true})
		return err == nil
	}},
	{"navigate+retry", func(t *testing.T, s *coding.Session, p *recordingProvider, id string) bool {
		if _, err := s.NavigateTree(t.Context(), id, coding.NavigateTreeOptions{}); err != nil {
			return false
		}
		// The next send fails with a retryable error; the session retries it.
		steps := []ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "overloaded_error"})}
		for range 4 {
			steps = append(steps, ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("after retry")}, StopReason: "stop"}))
		}
		p.SetResponses(steps)
		return true
	}},
	{"navigate+summary+resume+retry", func(t *testing.T, s *coding.Session, p *recordingProvider, id string) bool {
		if _, err := s.NavigateTree(t.Context(), id, coding.NavigateTreeOptions{Summarize: true}); err != nil {
			return false
		}
		if s.SwitchSession(s.Path()) != nil {
			return false
		}
		// The next send fails with a retryable error; the session retries it.
		steps := []ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "overloaded_error"})}
		for range 4 {
			steps = append(steps, ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("after retry")}, StopReason: "stop"}))
		}
		p.SetResponses(steps)
		return true
	}},
	{"navigate+clone", func(t *testing.T, s *coding.Session, _ *recordingProvider, id string) bool {
		if _, err := s.NavigateTree(t.Context(), id, coding.NavigateTreeOptions{Summarize: true}); err != nil {
			return false
		}
		return s.CloneInPlace() == nil
	}},
	{"fork-new-session", func(t *testing.T, s *coding.Session, _ *recordingProvider, id string) bool {
		for _, entry := range s.Entries() {
			if entry.Base().ID == id && entry.Base().Type == "message" {
				return s.ForkToNewSession(id) == nil
			}
		}
		return false
	}},
	{"fork", func(t *testing.T, s *coding.Session, _ *recordingProvider, id string) bool { return s.Fork(id) == nil }},
	{"navigate+compact", func(t *testing.T, s *coding.Session, _ *recordingProvider, id string) bool {
		if _, err := s.NavigateTree(t.Context(), id, coding.NavigateTreeOptions{}); err != nil {
			return false
		}
		_, err := s.Compact(t.Context(), "")
		return err == nil
	}},
	{"navigate+summary+resume", func(t *testing.T, s *coding.Session, _ *recordingProvider, id string) bool {
		if _, err := s.NavigateTree(t.Context(), id, coding.NavigateTreeOptions{Summarize: true}); err != nil {
			return false
		}
		return s.SwitchSession(s.Path()) == nil
	}},
}

// treeRoles are the message roles a session tree holds directly; summaries and compactions come from the operations.
var treeRoles = []string{"user", "assistant", "toolResult"}

// buildSessionTree appends generated conversation episodes to the session and, between episodes, moves the leaf to a random earlier entry (any entry, a tool result in the middle of parallel results included) and starts a new prompt there. It returns every entry id in creation order.
func buildSessionTree(t *testing.T, s *coding.Session, seed uint64) []string {
	t.Helper()
	g := newGenerator(seed, targets[0], Realistic)
	var ids []string
	flush := func(from int) int {
		for _, m := range g.msgs[from:] {
			if !slices.Contains(treeRoles, m["role"].(string)) {
				continue
			}
			encoded, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			var message agent.AgentMessage
			if err := json.Unmarshal(encoded, &message); err != nil {
				t.Fatal(err)
			}
			id, err := s.Inner().AppendMessage(message)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return len(g.msgs)
	}
	g.add(g.user())
	done := flush(0)
	for range 2 + g.pick(4) {
		g.episode()
		done = flush(done)
		if len(ids) > 2 && g.chance(0.5) {
			if err := s.Inner().Branch(ids[g.pick(len(ids))]); err != nil {
				t.Fatal(err)
			}
			g.add(g.user())
			done = flush(done)
		}
	}
	return ids
}

// recordingProvider records the transcript each request hands to the provider, before the provider's own transformMessages, and forwards it to the faux provider.
type recordingProvider struct {
	*ai.FauxProviderHandle
	mu       sync.Mutex
	requests [][]ai.Message
}

func (p *recordingProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, transcript.Messages())
	p.mu.Unlock()
	return p.FauxProviderHandle.Stream(ctx, transcript, options)
}

// handoffToolFlowError is "" when the tool results directly after an assistant message answer each of its tool calls exactly once and no tool result appears anywhere else. Anthropic Messages enforces it ("unexpected tool_use_id found in tool_result blocks ... Each tool_result block must have a corresponding tool_use block in the previous message"); Pi gets it from transformMessages, which closes pending calls at the next user turn (packages/ai/src/api/transform-messages.ts closePendingToolCalls); PiG's agent layer must hand providers that shape.
//
// The agent loop hands the stream function Pi's messages; a real provider repairs the tool flow (D48) and runs transformMessages, so the check reads the request after both.
func handoffToolFlowError(request []ai.Message) string {
	wire := ai.TransformMessages(ai.RepairToolFlow(request), &ai.Model{Input: []string{"text", "image"}}, nil)
	for i := 0; i < len(wire); i++ {
		switch message := wire[i].(type) {
		case ai.AssistantMessage:
			pending := map[string]bool{}
			for _, block := range message.Content {
				if call, ok := block.(ai.ToolCall); ok {
					pending[call.ID] = true
				}
			}
			j := i + 1
			for ; j < len(wire); j++ {
				result, ok := wire[j].(ai.ToolResultMessage)
				if !ok {
					break
				}
				if !pending[result.ToolCallID] {
					return fmt.Sprintf("message %d: tool result %q has no tool call in the previous assistant message", j, result.ToolCallID)
				}
				delete(pending, result.ToolCallID)
			}
			for id := range pending {
				return fmt.Sprintf("message %d: tool call %q is not answered directly after its assistant message", i, id)
			}
			i = j - 1
		case ai.ToolResultMessage:
			return fmt.Sprintf("message %d: tool result %q does not follow an assistant message", i, message.ToolCallID)
		}
	}
	return ""
}

func newOpsSession(t *testing.T, services *coding.AgentSessionServices) (*coding.Session, *recordingProvider) {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	steps := make([]ai.FauxResponseStep, 12)
	for i := range steps {
		steps[i] = ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(fmt.Sprintf("answer %d", i))}, StopReason: "stop"})
	}
	faux.SetResponses(steps)
	provider := &recordingProvider{FauxProviderHandle: faux}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 1000000}}
	s, err := coding.NewSession(services, coding.SessionOptions{Model: model, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, provider
}

// TestSessionOperationsSendValidRequests builds seeded session trees, runs each operation (navigate with and without a branch summary, fork, compact, resume) at every entry of the tree, sends a prompt, and requires that
//   - the request every provider receives meets the message contract (the checker of internal/requestvalidity, at the faux provider's send seam), and
//   - the history the session sent through PiG's pipeline produces the request Pi's pipeline produces for it (TestPipelineMatchesPi's rules).
//
// PIG_SESSION_SEEDS sets the trees (2 by default, 20 in CI).
func TestSessionOperationsSendValidRequests(t *testing.T) {
	trees := envInt("PIG_SESSION_SEEDS", map[bool]int{true: 20, false: 2}[os.Getenv("CI") != ""])
	first := uint64(envInt("PIG_PIPELINE_SEED", 1))
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"compaction":{"enabled":false,"keepRecentTokens":1},"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	collector := requestvalidity.CollectAll(t)
	h := newHarness(t)
	compared := 0
	ran := map[string]int{}
	skipped := map[string]int{}
	for tree := range trees {
		seed := first + uint64(tree)
		probe, _ := newOpsSession(t, services)
		points := len(buildSessionTree(t, probe, seed))
		for point := range points {
			for _, op := range sessionOps {
				s, provider := newOpsSession(t, services)
				ids := buildSessionTree(t, s, seed)
				if !op.apply(t, s, provider, ids[point]) {
					continue
				}
				ran[op.name]++
				history := sessionHistoryCase(t, s, seed, "continue from here")
				if o := h.judge(history); judgedBad(history, o, history.Mode) {
					encoded, _ := json.Marshal(history.Messages)
					t.Errorf("seed %d point %d %s: PiG's pipeline and Pi's differ on the session's history (%s): %s\npig violations: %v\npi violations: %v\nhistory: %s", seed, point, op.name, signature(o), truncate(o.Detail, 600), o.PigViolations, o.PiViolations, truncate(string(encoded), 3000))
				}
				compared++
				seen := len(collector.Violations())
				before := len(provider.requests)
				if _, err := s.Send(t.Context(), "continue from here"); err != nil {
					t.Errorf("seed %d point %d %s: Send: %v", seed, point, op.name, err)
					continue
				}
				if provider.CallCount() == 0 {
					// The operation replaced the session's model, so this send did not reach the faux provider.
					skipped[op.name]++
					continue
				}
				if strings.HasSuffix(op.name, "+retry") && len(provider.requests)-before < 2 {
					t.Errorf("seed %d point %d %s: the failed send was not retried (%d requests)", seed, point, op.name, len(provider.requests)-before)
				}
				for _, request := range provider.requests[min(before, len(provider.requests)):] {
					if problem := handoffToolFlowError(request); problem != "" {
						t.Errorf("seed %d point %d %s: the agent handed the provider a broken tool flow: %s", seed, point, op.name, problem)
					}
				}
				if violations := collector.Violations()[seen:]; len(violations) > 0 {
					t.Errorf("seed %d point %d %s: the provider received a request its contract rejects:\n  %s", seed, point, op.name, strings.Join(violations, "\n  "))
				}
			}
		}
	}
	t.Logf("operations run: %v; sends that did not reach the faux provider: %v; histories compared with Pi: %d", ran, skipped, compared)
	if ran["navigate"] == 0 || ran["navigate+summary"] == 0 || ran["fork"] == 0 {
		t.Fatalf("operations that must run did not: %v", ran)
	}
}

// sessionHistoryCase is the history the session's next Send hands its pipeline, the agent's messages plus the new prompt, as a differential case for the Anthropic target. Pi's pipeline receives the same messages, so the comparison holds PiG's convertToLlm, NormalizeMessages and converter to Pi's for every history a session operation produces.
func sessionHistoryCase(t *testing.T, s *coding.Session, seed uint64, prompt string) testCase {
	t.Helper()
	encoded, err := json.Marshal(s.Messages())
	if err != nil {
		t.Fatal(err)
	}
	var messages []message
	if err := json.Unmarshal(encoded, &messages); err != nil {
		t.Fatal(err)
	}
	messages = append(messages, message{"role": "user", "content": prompt, "timestamp": int64(1_800_000_000_000)})
	return testCase{Mode: Realistic, Seed: seed, Target: targets[0], Messages: messages}
}
