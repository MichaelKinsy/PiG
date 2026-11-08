package coding

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// upstream: agent-session.ts _handleAgentEvent (message_end goes to the extensions, then sessionManager.appendMessage(event.message) stores that object) and runner.ts emitMessageEnd (every handler holds one message; an edit of it in place is the message the entry stores).
//
// The whole wire, on a real Node extension: the Session numbers the messages of its events (RequestPayload.message_seqs), names the entries it persists (BindSessionReadActions getMessageEntries) and takes a handler's in-place edit of event.message back as the message's value. Paseo's Pi extension finds the entry of a message with `entry.message === message` at the assistant's message_start and at agent_end, even when another extension has edited the message. testdata/message-identity-recorder.mjs is that extension with an editor in front of it. The mutants this test is for: the host drops the numbers from the event requests, and the Session's getMessageEntries binding returns nothing.
func TestNodeExtensionFindsTheEntryOfItsMessagesAfterAnInPlaceEdit(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	source, err := filepath.Abs(filepath.Join("testdata", "message-identity-recorder.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("message identity Session complete") })
	var h *recoveryHarness
	var mu sync.Mutex
	var reports []string
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&boundaryNotifyUI{extension.NoopUIContext})
	bridge.SetNotifyFunc(func(message, _ string) {
		mu.Lock()
		reports = append(reports, message)
		mu.Unlock()
	})
	BindSessionReadActions(bridge, func() *Session {
		if h == nil {
			return nil
		}
		return h.session
	}, t.TempDir(), t.TempDir())
	host.SetUIBridge(bridge)
	loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "message-identity-recorder", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	h = newBoundaryHarness(t, harnessOptions{extension: *loaded}, boundaryReply("hello", ai.StopReasonStop, 0))
	boundaryPrompt(t, h, "go")

	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 1 {
		t.Fatalf("reports = %q, want one agent_end report", reports)
	}
	var report struct {
		UserIndexAtAssistantStart int  `json:"userIndexAtAssistantStart"`
		TurnEndHoldsAssistant     bool `json:"turnEndHoldsAssistant"`
		TurnEndAssistantBlocks    int  `json:"turnEndAssistantBlocks"`
		AgentEndHoldsUser         bool `json:"agentEndHoldsUser"`
		AgentEndHoldsAssistant    bool `json:"agentEndHoldsAssistant"`
		AgentEndUserBlocks        int  `json:"agentEndUserBlocks"`
		AgentEndAssistantBlocks   int  `json:"agentEndAssistantBlocks"`
		UserIndexAtAgentEnd       int  `json:"userIndexAtAgentEnd"`
		AssistantIndexAtAgentEnd  int  `json:"assistantIndexAtAgentEnd"`
		EntryUserBlocks           int  `json:"entryUserBlocks"`
		EntryAssistantBlocks      int  `json:"entryAssistantBlocks"`
	}
	if err := json.Unmarshal([]byte(reports[0]), &report); err != nil {
		t.Fatal(err)
	}
	if report.UserIndexAtAssistantStart < 0 || report.UserIndexAtAgentEnd < 0 || report.AssistantIndexAtAgentEnd < 0 {
		t.Errorf("entry.message === message found the user message at %d (assistant message_start) and %d (agent_end) and the assistant message at %d (agent_end), want entries: %s", report.UserIndexAtAssistantStart, report.UserIndexAtAgentEnd, report.AssistantIndexAtAgentEnd, reports[0])
	}
	if !report.TurnEndHoldsAssistant || !report.AgentEndHoldsUser || !report.AgentEndHoldsAssistant {
		t.Errorf("the message_end objects are not the turn_end and agent_end ones: %s", reports[0])
	}
	// The editor appended a block to each message in place: two blocks survive to every later payload and to the entry.
	for name, blocks := range map[string]int{
		"turn_end assistant": report.TurnEndAssistantBlocks, "agent_end user": report.AgentEndUserBlocks, "agent_end assistant": report.AgentEndAssistantBlocks,
		"entry user": report.EntryUserBlocks, "entry assistant": report.EntryAssistantBlocks,
	} {
		if blocks != 2 {
			t.Errorf("%s has %d content blocks, want 2 (the original and the in-place edit): %s", name, blocks, reports[0])
		}
	}
	for _, entry := range h.entries("message") {
		raw := string(entry.Raw())
		if (strings.Contains(raw, `"role":"user"`) || strings.Contains(raw, `"role":"assistant"`)) && !strings.Contains(raw, "(edited)") {
			t.Errorf("the saved entry lost the in-place edit: %s", raw)
		}
	}
}
