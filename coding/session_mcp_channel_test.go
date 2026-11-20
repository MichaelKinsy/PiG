package coding

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// A real stdio child, including a push immediately after initialized (before
// tools/list), so registration races cannot silently lose the first wake.
func TestMCPChannelStdioHelper(t *testing.T) {
	if os.Getenv("PIG_CHANNEL_HELPER") == "" {
		return
	}
	out := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
			caps := map[string]any{"tools": map[string]any{}}
			if os.Getenv("PIG_CHANNEL_DECLARE") == "true" {
				caps["experimental"] = map[string]any{"claude/channel": map[string]any{}}
			}
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": caps, "serverInfo": map[string]any{"name": "channel-test", "version": "1"}}
		case "notifications/initialized":
			for _, content := range []string{"first", "second"} {
				_ = out.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/claude/channel", "params": map[string]any{"content": content, "meta": map[string]any{"event_id": content, "bad-key": "drop", "source": "spoof", "nonstring": 17}}})
			}
			continue
		case "tools/list":
			result = map[string]any{"tools": []any{}}
		}
		if request.ID != nil {
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}
	os.Exit(0)
}

func channelTestConnection(t *testing.T, session *Session, optIn, declared bool, names ...string) *mcpext.Connection {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := extension.NewOrderedStrings("PIG_CHANNEL_HELPER", "1", "PIG_CHANNEL_DECLARE", fmt.Sprint(declared))
	name := "test-server"
	if len(names) != 0 {
		name = names[0]
	}
	connection, err := mcpext.NewConnection(mcpext.ConnectionOptions{
		Entry: mcpext.McpServerEntry{Name: name, Channel: optIn, Config: extension.McpServerConfig{Command: exe, Args: []string{"-test.run=^TestMCPChannelStdioHelper$"}, Env: env}},
		Cwd:   t.TempDir(), CreateTransport: mcpext.CreateDefaultTransport,
		OnChannel: func(message extension.SendMessagePayload, options *extension.SendMessageOptions) {
			_ = session.SendMessage(extension.CustomMessageRef(message), options)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	if _, err := connection.GetClient(t.Context()); err != nil {
		t.Fatal(err)
	}
	return connection
}

func channelTexts(session *Session) []string {
	var texts []string
	for _, message := range session.Messages() {
		if message.Custom != nil && message.Custom["customType"] == "mcp-channel" {
			texts = append(texts, fmt.Sprint(message.Custom["content"]))
		}
	}
	return texts
}

func TestMCPChannelStdioIdleAndJSONLProvenance(t *testing.T) {
	h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	h.session.Inner().SetPath(path)
	h.provider.responses = []scriptedResponse{fauxReply("received", ai.StopReasonStop, 0), fauxReply("received next", ai.StopReasonStop, 0), fauxReply("user", ai.StopReasonStop, 0)}
	channelTestConnection(t, h.session, true, true)
	h.settle(t)
	if err := h.session.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	texts := channelTexts(h.session)
	want := []string{`<channel source="test-server" event_id="first">first</channel>`, `<channel source="test-server" event_id="second">second</channel>`}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("channel texts=%q", texts)
	}
	// Identical visible input typed by a user must retain a different JSONL shape.
	if err := h.session.Prompt(t.Context(), texts[0], nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	custom, user := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry struct {
			Type             string `json:"type"`
			CustomType       string `json:"customType"`
			Content          string `json:"content"`
			MCPChannelSource string `json:"mcpChannelSource"`
			Details          struct {
				Source string            `json:"source"`
				Meta   map[string]string `json:"meta"`
			} `json:"details"`
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.CustomType == "mcp-channel" {
			custom++
			if entry.Type != "custom_message" || entry.MCPChannelSource != "test-server" || entry.Details.Source != "test-server" || len(entry.Details.Meta) != 1 || !strings.HasPrefix(entry.Content, `<channel source="test-server"`) {
				t.Fatalf("bad provenance: %s", line)
			}
		}
		if entry.Type == "message" && entry.Message.Role == agent.RoleUser {
			user++
			if entry.MCPChannelSource != "" {
				t.Fatalf("user message gained channel provenance: %s", line)
			}
		}
	}
	if custom != 2 || user != 1 {
		t.Fatalf("custom=%d user=%d JSONL=%s", custom, user, data)
	}
}

func TestMCPChannelStdioBusyFIFO(t *testing.T) {
	waiting := createQueueWaitingHarness(t, extension.Extension{})
	h := waiting.h
	waiting.setResponses(fauxToolCall("wait"), func(messages []ai.Message) *ai.AssistantMessage {
		texts := strings.Join(queueProviderUserTexts(messages), "\n")
		if !strings.Contains(texts, ">first</channel>") || !strings.Contains(texts, ">second</channel>") {
			t.Errorf("queued channels did not arrive as one group: %s", texts)
		}
		return fauxReply("after tool", ai.StopReasonStop, 0)(messages)
	})
	<-waiting.waitForToolStart
	channelTestConnection(t, h.session, true, true)
	if texts := channelTexts(h.session); len(texts) != 0 {
		t.Fatalf("delivered inside tool: %q", texts)
	}
	if count := h.provider.callCount(); count != 1 {
		t.Fatalf("interrupted tool: calls=%d", count)
	}
	waiting.releaseToolExecution()
	waiting.join()
	if count := h.provider.callCount(); count != 2 {
		t.Fatalf("expected one grouped continuation, calls=%d", count)
	}
	texts := channelTexts(h.session)
	if len(texts) != 2 || !strings.Contains(texts[0], ">first</channel>") || !strings.Contains(texts[1], ">second</channel>") {
		t.Fatalf("FIFO=%q", texts)
	}
}

func TestNonMCPChannelNameDoesNotGroup(t *testing.T) {
	waiting := createQueueWaitingHarness(t, extension.Extension{})
	h := waiting.h
	waiting.setResponses(fauxToolCall("wait"), fauxReply("first ordinary message", ai.StopReasonStop, 0), fauxReply("second ordinary message", ai.StopReasonStop, 0))
	<-waiting.waitForToolStart
	for _, text := range []string{"first", "second"} {
		message := extension.CustomMessageRef{
			CustomType: "mcp-channel", Content: `<channel source="extension">` + text + `</channel>`, Display: true,
			Details: map[string]any{"source": "extension", "meta": map[string]any{}, "mcpChannelSource": "extension"},
		}
		if err := h.session.SendMessage(message, &extension.SendMessageOptions{DeliverAs: extension.DeliverAsSteer}); err != nil {
			t.Fatal(err)
		}
	}
	waiting.join()
	if got := h.provider.callCount(); got != 3 {
		t.Fatalf("provider calls = %d, want initial call and one continuation per ordinary message", got)
	}
}

func TestMCPChannelStdioAcrossServers(t *testing.T) {
	waiting := createQueueWaitingHarness(t, extension.Extension{})
	h := waiting.h
	waiting.setResponses(fauxToolCall("wait"), fauxReply("both servers", ai.StopReasonStop, 0))
	<-waiting.waitForToolStart
	// GetClient's tools/list response is a barrier after this child's pushes.
	channelTestConnection(t, h.session, true, true, "server-a")
	channelTestConnection(t, h.session, true, true, "server-b")
	waiting.releaseToolExecution()
	waiting.join()
	texts := channelTexts(h.session)
	if len(texts) != 4 {
		t.Fatalf("texts=%q", texts)
	}
	for i, source := range []string{"server-a", "server-a", "server-b", "server-b"} {
		if !strings.Contains(texts[i], `source="`+source+`"`) {
			t.Fatalf("cross-server arrival order=%q", texts)
		}
	}
	if count := h.provider.callCount(); count != 2 {
		t.Fatalf("cross-server group split into extra requests: %d", count)
	}
}

func TestMCPChannelStdioGates(t *testing.T) {
	for _, gates := range []struct{ optIn, declared bool }{{false, true}, {true, false}, {false, false}} {
		t.Run(fmt.Sprint(gates), func(t *testing.T) {
			h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
			channelTestConnection(t, h.session, gates.optIn, gates.declared)
			if h.session.IsStreaming() || len(h.session.Messages()) != 0 || h.provider.callCount() != 0 {
				t.Fatal("unapproved channel reached session")
			}
		})
	}
}
