package coding

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:200,247,280,337,366
func TestUpstreamSessionPromptExpansion(t *testing.T) {
	t.Run("expands skill commands before sending the prompt", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test-skill.md")
		if err := os.WriteFile(path, []byte("# Test Skill\n\nUse the skill body."), 0o600); err != nil {
			t.Fatal(err)
		}
		h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
		h.session.SetPromptResources(nil, []*Skill{{Name: "test", Description: "Test skill", FilePath: path, BaseDir: dir}})
		var expanded string
		h.provider.responses = []scriptedResponse{func(messages []ai.Message) *ai.AssistantMessage {
			expanded = modelExtensionUserText(messages)
			return fauxReply("ok", ai.StopReasonStop, 0)(messages)
		}}
		if err := h.session.Prompt(t.Context(), "/skill:test explain this"); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`<skill name="test" location="`, "Use the skill body.", "explain this"} {
			if !strings.Contains(expanded, want) {
				t.Errorf("expanded=%q does not contain %q", expanded, want)
			}
		}
	})
	for _, userMessage := range []bool{false, true} {
		name := "expands prompt templates before sending the prompt"
		if userMessage {
			name = "sendUserMessage can opt into prompt template expansion"
		}
		t.Run(name, func(t *testing.T) {
			h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
			h.session.SetPromptResources([]PromptTemplate{{Name: "review", Description: "Review template", Content: "Review this code: $1", FilePath: "/virtual/review.md"}}, nil)
			var expanded string
			h.provider.responses = []scriptedResponse{func(messages []ai.Message) *ai.AssistantMessage {
				expanded = modelExtensionUserText(messages)
				return fauxReply("ok", ai.StopReasonStop, 0)(messages)
			}}
			var err error
			if userMessage {
				err = h.session.SendUserMessage(t.Context(), "/review src/index.ts", &extension.SendUserMessageOptions{ExpandPromptTemplates: new(true)})
			} else {
				err = h.session.Prompt(t.Context(), "/review src/index.ts")
			}
			if err != nil {
				t.Fatal(err)
			}
			if expanded != "Review this code: src/index.ts" {
				t.Fatalf("expanded=%q", expanded)
			}
		})
	}
	t.Run("extension sendUserMessage can opt into extension command dispatch", func(t *testing.T) {
		commandRun := make(chan string, 1)
		h := newQueueCharacterizationHarness(t, queueCommandExtension(func(_ context.Context, args string) error { commandRun <- args; return nil }), nil)
		api := h.session.currentRunner().CreateCommandContext()
		if err := api.SendUserMessage("/testcmd hello world", &extension.SendUserMessageOptions{ExpandPromptTemplates: new(true)}); err != nil {
			t.Fatal(err)
		}
		select {
		case args := <-commandRun:
			if args != "hello world" {
				t.Fatalf("command=%q", args)
			}
		default:
			t.Fatal("extension command was not dispatched")
		}
		if len(h.session.Messages()) != 0 || h.provider.callCount() != 0 {
			t.Fatalf("messages=%v calls=%d", h.session.Messages(), h.provider.callCount())
		}
	})
	t.Run("sendUserMessage while idle triggers a turn", func(t *testing.T) {
		h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
		h.provider.responses = []scriptedResponse{fauxReply("response", ai.StopReasonStop, 0)}
		if err := h.session.SendUserMessage(t.Context(), "from extension", nil); err != nil {
			t.Fatal(err)
		}
		roles := promptCharacterizationRoles(h)
		if !slices.Equal(roles, []string{"system", "user", "assistant"}) {
			t.Fatalf("roles=%v", roles)
		}
		if text := extractUserMessageText(h.session.Messages()[1].User.Content); text != "from extension" {
			t.Fatalf("text=%q", text)
		}
	})
}
