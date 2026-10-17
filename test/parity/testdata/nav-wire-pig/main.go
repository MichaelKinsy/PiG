// Prints the tool flow of the Anthropic request that follows /tree navigation. It builds the tree below, navigates to every entry with and without a branch summary, sends a prompt through PiG's Session and prints, for each case, the number of requests the server received, the roles, texts and tool call/result ids of the prompt's request body, and whether the Anthropic tool_result contract holds. nav-wire-pi.mjs prints the same for Pi.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

const sse = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"abandoned branch summary\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

type server struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, sse)
}

func (s *server) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func (s *server) last() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[len(s.bodies)-1]
}

type builder struct {
	sm  *coding.SessionManager
	ids []string
}

func (b *builder) add(m agent.AgentMessage) string {
	id, err := b.sm.AppendMessage(m)
	if err != nil {
		panic(err)
	}
	b.ids = append(b.ids, id)
	return id
}

func (b *builder) user(text string) string {
	return b.add(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}})
}

// foreign marks an assistant message as produced by another model, as nav-wire-pi.mjs does, so both pipelines take the same cross-model branch of transform-messages.
func foreign(m *agent.AssistantMessage) *agent.AssistantMessage {
	m.API, m.Provider, m.ModelID = ai.APIOpenAIResponses, "other", "other-1"
	return m
}

func (b *builder) text(text string) string {
	return b.add(agent.AgentMessage{Assistant: foreign(&agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: ai.StopReasonStop})})
}

func (b *builder) calls(stop ai.StopReason, ids ...string) string {
	blocks := make([]ai.AssistantContentBlock, 0, len(ids))
	for _, id := range ids {
		blocks = append(blocks, ai.ToolCall{ID: id, Name: "read", Arguments: ai.JsonObject{"path": id}})
	}
	return b.add(agent.AgentMessage{Assistant: foreign(&agent.AssistantMessage{Role: agent.RoleAssistant, Content: blocks, StopReason: stop})})
}

func (b *builder) result(id string) string {
	return b.add(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: id, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "result " + id}}}})
}

func (b *builder) forkAt(id string) {
	if err := b.sm.SetLeafID(&id); err != nil {
		panic(err)
	}
}

// tree is the forked tree of coding/session_tree_wire_oracle_test.go: parallel calls, single calls, a branch from between parallel results, a branch at the call message. nav-wire-pi.mjs builds the same tree.
func (b *builder) tree() {
	b.user("start")
	a1 := b.calls(ai.StopReasonToolUse, "c1", "c2", "c3")
	r1 := b.result("c1")
	b.result("c2")
	b.result("c3")
	b.text("done reading")
	b.user("next")
	b.calls(ai.StopReasonToolUse, "d1")
	b.result("d1")
	b.text("done again")

	b.forkAt(r1)
	b.user("retry after c1")
	b.calls(ai.StopReasonToolUse, "e1", "e2")
	b.result("e1")
	b.result("e2")
	b.text("done retry")

	b.forkAt(a1)
	b.user("other idea")
	b.text("other answer")
	// An errored turn whose result was persisted is left out: Pi sends that orphan tool_result (HTTP 400) and PiG drops it (D48), so the pair differs by design there.
}

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// jsString is JSON.stringify of a string: Go's encoder without HTML escaping, which writes the same bytes for the texts this program sends.
func jsString(text string) string {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	return strings.TrimSuffix(out.String(), "\n")
}

// appendNew adds id unless ids holds it: a JavaScript Set's add.
func appendNew(ids []string, id string) []string {
	if slices.Contains(ids, id) {
		return ids
	}
	return append(ids, id)
}

// resultText joins the text of a tool_result's content, a string or text blocks.
func resultText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	blocks, _ := content.([]any)
	var out strings.Builder
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if text, ok := block["text"].(string); ok && block["type"] == "text" {
			out.WriteString(text)
		} else {
			out.WriteString("[" + fmt.Sprint(block["type"]) + "]")
		}
	}
	return out.String()
}

// flow renders the request's messages as "role[kind:id,text=\"...\",...]" and checks the Anthropic rule: the tool_result blocks of a user message answer exactly the tool_use blocks of the assistant message right before it.
func flow(body []byte) (string, string) {
	var request struct {
		Messages []wireMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return "unparsable", "INVALID: " + err.Error()
	}
	var out []string
	// Ids keep block order, like the JavaScript Sets of nav-wire-pi.mjs, so a failing case names the same id on both sides.
	var open []string
	problem := ""
	for i, m := range request.Messages {
		var blocks []map[string]any
		if json.Unmarshal(m.Content, &blocks) != nil {
			var text string
			_ = json.Unmarshal(m.Content, &text)
			blocks = []map[string]any{{"type": "text", "text": text}}
		}
		var parts []string
		var uses, results []string
		for _, block := range blocks {
			switch block["type"] {
			case "tool_use":
				id, _ := block["id"].(string)
				uses = appendNew(uses, id)
				parts = append(parts, "tool_use:"+id)
			case "tool_result":
				id, _ := block["tool_use_id"].(string)
				results = appendNew(results, id)
				flag := ""
				if isError, _ := block["is_error"].(bool); isError {
					flag = ":error"
				}
				parts = append(parts, "tool_result:"+id+flag+"="+jsString(resultText(block["content"])))
			case "text":
				text, _ := block["text"].(string)
				parts = append(parts, "text="+jsString(text))
			default:
				parts = append(parts, fmt.Sprint(block["type"]))
			}
		}
		for _, id := range results {
			if !slices.Contains(open, id) && problem == "" {
				problem = fmt.Sprintf("INVALID: message %d answers \"%s\", which the message before it did not call", i, id)
			}
		}
		for _, id := range open {
			if (m.Role == "assistant" || !slices.Contains(results, id)) && problem == "" {
				problem = fmt.Sprintf("INVALID: message %d follows a call to \"%s\" that no tool_result answers", i, id)
			}
		}
		open = uses
		out = append(out, m.Role+"["+strings.Join(parts, ",")+"]")
	}
	if len(open) > 0 && problem == "" {
		problem = "INVALID: the request ends on an unanswered tool call"
	}
	if problem == "" {
		problem = "valid"
	}
	return strings.Join(out, " "), problem
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	srv := &server{}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	home, err := os.MkdirTemp("", "nav-wire-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(home) }()
	agentDir := filepath.Join(home, "agent")
	// An agent-directory variable inherited from the caller wins over PIG_HOME, so every one of them points into the scratch home.
	for name, value := range map[string]string{"HOME": home, "PIG_HOME": home, "PI_HOME": home, "PIG_CODING_AGENT_DIR": agentDir, "PI_CODING_AGENT_DIR": agentDir} {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return err
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: home, AgentDir: agentDir})
	if err != nil {
		return err
	}
	defer services.Close()

	openSession := func(sm *coding.SessionManager) (*coding.Session, error) {
		provider := ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: "test-key", Model: "claude-sonnet-4-5", BaseURL: ts.URL, ProviderID: "anthropic"})
		model := &ai.Model{ID: "claude-sonnet-4-5", DisplayName: "claude-sonnet-4-5", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 200000, SupportsToolUse: true}}
		return coding.NewSession(services, coding.SessionOptions{Model: model, SessionManager: sm, SkipBuiltinTools: true})
	}
	// buildSession mirrors nav-wire-pi.mjs: a new session records its model and thinking level, the tree is appended to its session manager, and the session that navigates opens on that manager.
	buildSession := func() (*coding.Session, []string, error) {
		sm, err := coding.NewInMemorySessionManager(home)
		if err != nil {
			return nil, nil, err
		}
		first, err := openSession(sm)
		if err != nil {
			return nil, nil, err
		}
		if err := first.Close(); err != nil {
			return nil, nil, err
		}
		b := &builder{sm: sm}
		b.tree()
		sess, err := openSession(sm)
		return sess, b.ids, err
	}
	probe, entries, err := buildSession()
	if err != nil {
		return err
	}
	_ = probe.Close()

	for index := range entries {
		for _, summarize := range []bool{false, true} {
			sess, ids, err := buildSession()
			if err != nil {
				return err
			}
			go func() {
				for range sess.Events() {
				}
			}()
			before := srv.count()
			if _, err := sess.NavigateTree(context.Background(), ids[index], coding.NavigateTreeOptions{Summarize: summarize}); err != nil {
				return fmt.Errorf("navigate entry %d: %w", index, err)
			}
			if _, err := sess.Send(context.Background(), "next prompt"); err != nil {
				return fmt.Errorf("send entry %d: %w", index, err)
			}
			shape, verdict := flow(srv.last())
			fmt.Printf("entry %02d summary=%v requests=%d: %s => %s\n", index, summarize, srv.count()-before, shape, verdict)
			_ = sess.Close()
		}
	}
	return nil
}
