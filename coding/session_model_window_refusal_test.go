// SPDX-License-Identifier: MIT
package coding

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// windowRefusalSession returns a Session whose current model has an 8000-token window and whose transcript reports tokens tokens of
// context (tokens < 0 leaves the usage unknown after a compaction), plus a target model with a 128000-token window.
func windowRefusalSession(t *testing.T, tokens int) (*Session, *ai.Model) {
	t.Helper()
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	if tokens >= 0 {
		usage := &ai.Usage{Input: tokens, Output: 0, TotalTokens: tokens}
		assistant := &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "seed"}}, StopReason: ai.StopReasonStop, Usage: usage}
		if _, err := sess.Inner().AppendMessage(agent.AgentMessage{Assistant: assistant}); err != nil {
			t.Fatal(err)
		}
	} else {
		old := &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "old"}}, StopReason: ai.StopReasonStop, Usage: &ai.Usage{Input: 10000, TotalTokens: 10001}}
		id, err := sess.Inner().AppendMessage(agent.AgentMessage{Assistant: old})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sess.Inner().AppendCompaction("summary", id, 10001, nil, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	target := fakeModel()
	target.ID = "small-window"
	target.Capabilities.ContextWindow = 128000
	return sess, target
}

func windowRefusalMessage(target *ai.Model, tokens int) string {
	return fmt.Sprintf("%s/%s holds %d tokens; this session has %d. Run /compact first, then switch.", providerID(target), target.ID, target.Capabilities.ContextWindow, tokens)
}

// windowRefusalSnapshot is the state a refused switch must leave alone.
type windowRefusalSnapshot struct {
	modelID  string
	thinking ai.ModelThinkingLevel
	entries  int
}

func takeWindowRefusalSnapshot(sess *Session) windowRefusalSnapshot {
	return windowRefusalSnapshot{modelID: sess.Model().ID, thinking: sess.ThinkingLevel(), entries: len(sess.Inner().GetEntries())}
}

func windowRefusalModelChanges(sess *Session) int {
	n := 0
	for _, entry := range sess.Inner().GetEntries() {
		if entry.Base().Type == "model_change" {
			n++
		}
	}
	return n
}

// R1: a user switch to a model whose window is below the session's tokens is refused before any mutation.
func TestUserModelSwitchRefusedWhenTokensExceedTargetWindow(t *testing.T) {
	paths := map[string]func(*Session, *ai.Model) error{
		"SetModel": func(s *Session, m *ai.Model) error { return s.SetModel(m) },
		"SetModelOnMain": func(s *Session, m *ai.Model) error {
			return s.SetModelOnMain(context.Background(), m, ModelMutationOptions{}, nil)
		},
		"BeginModelChange(user)": func(s *Session, m *ai.Model) error {
			_, err := s.BeginModelChange(context.Background(), m, extension.ModelSelectSourceUser)
			return err
		},
	}
	for name, do := range paths {
		t.Run(name, func(t *testing.T) {
			sess, target := windowRefusalSession(t, 150000)
			before := takeWindowRefusalSnapshot(sess)
			changes := windowRefusalModelChanges(sess)
			err := do(sess, target)
			if err == nil {
				t.Fatal("switch to a smaller window was not refused")
			}
			if got, want := err.Error(), windowRefusalMessage(target, 150000); got != want {
				t.Fatalf("message = %q, want %q", got, want)
			}
			if after := takeWindowRefusalSnapshot(sess); after != before {
				t.Fatalf("refused switch mutated state: before %+v after %+v", before, after)
			}
			if windowRefusalModelChanges(sess) != changes {
				t.Fatal("refused switch appended a model_change entry")
			}
		})
	}
}

// R2: tokens below the target window switch as today.
func TestUserModelSwitchAllowedWhenTokensFitTargetWindow(t *testing.T) {
	sess, target := windowRefusalSession(t, 100000)
	if err := sess.SetModel(target); err != nil {
		t.Fatal(err)
	}
	if sess.Model().ID != target.ID {
		t.Fatalf("model = %s, want %s", sess.Model().ID, target.ID)
	}
}

// R3: unknown tokens (after a compaction) never refuse.
func TestUserModelSwitchAllowedWhenTokensUnknown(t *testing.T) {
	sess, target := windowRefusalSession(t, -1)
	if usage := sess.ContextUsage(); usage == nil || usage.Tokens != nil {
		t.Fatalf("usage = %#v, want unknown tokens", usage)
	}
	if err := sess.SetModel(target); err != nil {
		t.Fatal(err)
	}
	if sess.Model().ID != target.ID {
		t.Fatalf("model = %s, want %s", sess.Model().ID, target.ID)
	}
}

// R4: an extension-sourced switch is not refused.
func TestExtensionModelSwitchAllowedWhenTokensExceedTargetWindow(t *testing.T) {
	sess, target := windowRefusalSession(t, 150000)
	// ollama needs no configured auth, so the extension path reaches the switch.
	target.ProviderMeta.ProviderID = "ollama"
	switched, err := sess.ExtensionSetModel(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if !switched || sess.Model().ID != target.ID {
		t.Fatalf("switched = %v, model = %s", switched, sess.Model().ID)
	}
}

// R5: a target with an unknown (zero) window is not refused.
func TestUserModelSwitchAllowedWhenTargetWindowUnknown(t *testing.T) {
	sess, target := windowRefusalSession(t, 150000)
	target.Capabilities.ContextWindow = 0
	if err := sess.SetModel(target); err != nil {
		t.Fatal(err)
	}
	if sess.Model().ID != target.ID {
		t.Fatalf("model = %s, want %s", sess.Model().ID, target.ID)
	}
}
