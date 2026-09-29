package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The tool barrier retains Pi's awaited execution lifetime without timer-based ordering.
type queuedSlashWaitTool struct {
	started  chan struct{}
	released <-chan struct{}
}

func (*queuedSlashWaitTool) Name() string                           { return "wait" }
func (*queuedSlashWaitTool) Label() string                          { return "Wait" }
func (*queuedSlashWaitTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (*queuedSlashWaitTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "wait", Description: "Wait for the test to release execution", Parameters: map[string]any{"type": "object"}}
}
func (tool *queuedSlashWaitTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	close(tool.started)
	select {
	case <-tool.released:
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "released"}}, Details: map[string]any{}}, nil
	case <-ctx.Done():
		return agent.AgentToolResult{}, ctx.Err()
	}
}

func queuedSlashUserTexts(messages []agent.AgentMessage) []string {
	var texts []string
	for _, message := range messages {
		if message.User != nil {
			var parts []string
			for _, block := range message.ContentBlocks() {
				if text, ok := block.(ai.TextContent); ok {
					parts = append(parts, text.Text)
				}
			}
			texts = append(texts, strings.Join(parts, "\n"))
		}
	}
	return texts
}

func awaitQueuedSlashSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("expected operation did not start")
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/2023-queued-slash-command-followup.test.ts:17
func TestExtensionQueuedSlashFollowUpIsRawUserTextUpstream(t *testing.T) {
	var commandRuns []string
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	tool := &queuedSlashWaitTool{started: make(chan struct{}), released: release}
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{}, Commands: map[string]extension.RegisteredCommand{"testcmd": {Name: "testcmd", Description: "Test command", Handler: func(_ context.Context, args string) error { commandRuns = append(commandRuns, args); return nil }}}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext, tools: []agent.AgentTool{tool}}, fauxToolCall("wait"), fauxReply("first turn complete", ai.StopReasonStop, 0), fauxReply("queued follow-up handled by model", ai.StopReasonStop, 0))
	done := make(chan error, 1)
	go func() { _, err := h.session.Send(t.Context(), "start"); done <- err }()
	awaitQueuedSlashSignal(t, tool.started)
	if err := h.session.SendExtensionUserMessage("/testcmd queued", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp}); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(commandRuns) != 0 {
		t.Fatalf("queued extension command dispatched: %q", commandRuns)
	}
	if got := queuedSlashUserTexts(h.session.Messages()); !reflect.DeepEqual(got, []string{"start", "/testcmd queued"}) {
		t.Fatalf("user texts=%q", got)
	}
	if got := lastAssistantText(h.session.Messages()); got != "queued follow-up handled by model" {
		t.Fatalf("assistant text=%q", got)
	}
}
