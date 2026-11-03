package codingagent

// The Pig half of the handleEvent differential: the corpus test/parity/testdata/handle-event-corpus.json runs through the production owner-loop event
// handler, and the trace is printed in the shape test/parity/testdata/handle-event-pi.mjs prints for the pinned Pi InteractiveMode.handleEvent.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type handleEventStep struct {
	Name     string         `json:"name"`
	Event    *handleEventIn `json:"event"`
	Op       string         `json:"op"`
	Steering []string       `json:"steering"`
	FollowUp []string       `json:"followUp"`
	Level    string         `json:"level"`
	Visible  bool           `json:"visible"`
}

type handleEventIn struct {
	Type         string          `json:"type"`
	TurnIndex    int             `json:"turnIndex"`
	Reason       string          `json:"reason"`
	Aborted      bool            `json:"aborted"`
	WillRetry    bool            `json:"willRetry"`
	ErrorMessage string          `json:"errorMessage"`
	Attempt      int             `json:"attempt"`
	MaxAttempts  int             `json:"maxAttempts"`
	DelayMs      int             `json:"delayMs"`
	Success      bool            `json:"success"`
	FinalError   string          `json:"finalError"`
	Source       string          `json:"source"`
	Name         string          `json:"name"`
	Steering     []string        `json:"steering"`
	FollowUp     []string        `json:"followUp"`
	Level        string          `json:"level"`
	ID           *string         `json:"id"`
	Delta        string          `json:"delta"`
	Entry        json.RawMessage `json:"entry"`

	Message               *probeMessage   `json:"message"`
	AssistantMessageEvent *probeStreamed  `json:"assistantMessageEvent"`
	ToolCallID            string          `json:"toolCallId"`
	ToolName              string          `json:"toolName"`
	ParentToolCallID      string          `json:"parentToolCallId"`
	Args                  json.RawMessage `json:"args"`
	PartialResult         *probeResult    `json:"partialResult"`
	Result                *probeResult    `json:"result"`
	IsError               bool            `json:"isError"`
	DurationMs            *int64          `json:"durationMs"`
}

type probeBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type probeMessage struct {
	Role         string       `json:"role"`
	Content      []probeBlock `json:"content"`
	StopReason   string       `json:"stopReason"`
	ErrorMessage string       `json:"errorMessage"`
}

type probeStreamed struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

type probeResult struct {
	Content []probeBlock `json:"content"`
}

func (b probeBlock) assistantBlock() ai.AssistantContentBlock {
	if b.Type == "toolCall" {
		return ai.ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Arguments}
	}
	return ai.TextContent{Text: b.Text}
}

func (m *probeMessage) agentMessage() agent.AgentMessage {
	if m.Role == "user" {
		return userMsg(m.Content[0].Text)
	}
	blocks := make([]ai.AssistantContentBlock, 0, len(m.Content))
	for _, block := range m.Content {
		blocks = append(blocks, block.assistantBlock())
	}
	message := assistantMsg("", blocks...)
	message.Assistant.StopReason = ai.StopReason(m.StopReason)
	message.Assistant.ErrorMessage = m.ErrorMessage
	return message
}

func (m *probeMessage) partial() *ai.AssistantMessage {
	blocks := make([]ai.AssistantContentBlock, 0, len(m.Content))
	for _, block := range m.Content {
		blocks = append(blocks, block.assistantBlock())
	}
	return &ai.AssistantMessage{Content: blocks}
}

func (r *probeResult) toolResult() agent.AgentToolResult {
	result := agent.AgentToolResult{}
	for _, block := range r.Content {
		result.Content = append(result.Content, ai.TextContent{Text: block.Text})
	}
	return result
}

func (in *handleEventIn) agentEvent(t *testing.T) agent.AgentEvent {
	switch in.Type {
	case "agent_start":
		return agent.AgentStartEvent{}
	case "agent_end":
		return agent.AgentEndEvent{}
	case "turn_start":
		return agent.TurnStartEvent{TurnIndex: in.TurnIndex}
	case "turn_end":
		return agent.TurnEndEvent{TurnIndex: in.TurnIndex}
	case "agent_settled":
		return agent.AgentSettledEvent{Aborted: in.Aborted}
	case "queue_update":
		return agent.QueueUpdateEvent{Steering: in.Steering, FollowUp: in.FollowUp}
	case "thinking_level_changed":
		return agent.ThinkingLevelChangedEvent{Level: ai.ModelThinkingLevel(in.Level)}
	case "bash_execution_update":
		return agent.BashExecutionUpdateEvent{ID: in.ID, Delta: in.Delta}
	case "session_info_changed":
		return agent.SessionInfoChangedEvent{Name: in.Name}
	case "entry_appended":
		return agent.EntryAppendedEvent{Entry: in.Entry}
	case "compaction_start":
		return agent.CompactionStartEvent{Reason: in.Reason}
	case "compaction_end":
		return agent.CompactionEndEvent{Reason: in.Reason, Aborted: in.Aborted, WillRetry: in.WillRetry, ErrorMessage: in.ErrorMessage}
	case "auto_retry_start":
		return agent.AutoRetryStartEvent{Attempt: in.Attempt, MaxAttempts: in.MaxAttempts, DelayMs: in.DelayMs, ErrorMessage: in.ErrorMessage}
	case "auto_retry_end":
		return agent.AutoRetryEndEvent{Success: in.Success, Attempt: in.Attempt, FinalError: in.FinalError}
	case "summarization_retry_scheduled":
		return agent.SummarizationRetryScheduledEvent{Attempt: in.Attempt, MaxAttempts: in.MaxAttempts, DelayMs: in.DelayMs, ErrorMessage: in.ErrorMessage}
	case "summarization_retry_attempt_start":
		return agent.SummarizationRetryAttemptStartEvent{Source: in.Source, Reason: in.Reason}
	case "summarization_retry_finished":
		return agent.SummarizationRetryFinishedEvent{}
	case "message_start":
		return agent.MessageStartEvent{Message: in.Message.agentMessage()}
	case "message_end":
		return agent.MessageEndEvent{Message: in.Message.agentMessage()}
	case "message_update":
		var streamed ai.AssistantMessageEvent
		switch in.AssistantMessageEvent.Type {
		case "text_delta":
			streamed = ai.TextDeltaEvent{ContentIndex: in.AssistantMessageEvent.ContentIndex, Delta: in.AssistantMessageEvent.Delta}
		case "toolcall_start":
			streamed = ai.ToolCallStartEvent{ContentIndex: in.AssistantMessageEvent.ContentIndex, Partial: in.Message.partial()}
		case "toolcall_delta":
			streamed = ai.ToolCallDeltaEvent{ContentIndex: in.AssistantMessageEvent.ContentIndex, Delta: in.AssistantMessageEvent.Delta, Partial: in.Message.partial()}
		default:
			t.Fatalf("unknown streamed event %q", in.AssistantMessageEvent.Type)
		}
		return agent.MessageUpdateEvent{Message: in.Message.agentMessage(), AssistantMessageEvent: streamed}
	case "tool_execution_start":
		return agent.ToolExecutionStartEvent{ToolCallID: in.ToolCallID, ParentToolCallID: in.ParentToolCallID, ToolName: in.ToolName, Args: in.Args}
	case "tool_execution_update":
		return agent.ToolExecutionUpdateEvent{ToolCallID: in.ToolCallID, ToolName: in.ToolName, Args: in.Args, PartialResult: in.PartialResult.toolResult()}
	case "tool_execution_end":
		return agent.ToolExecutionEndEvent{ToolCallID: in.ToolCallID, ToolName: in.ToolName, Result: in.Result.toolResult(), IsError: in.IsError, DurationMs: in.DurationMs}
	}
	t.Fatalf("unknown corpus event %q", in.Type)
	return nil
}

type handleEventRecord struct {
	Step     string   `json:"step"`
	Esc      string   `json:"esc,omitempty"`
	Progress []bool   `json:"progress"`
	Status   string   `json:"status"`
	Pending  string   `json:"pending"`
	Border   string   `json:"border"`
	Titles   []string `json:"titles"`
	Shutdown bool     `json:"shutdown"`
	Chat     string   `json:"chat"`
}

type handleEventTrace struct {
	Name         string              `json:"name"`
	ShowProgress bool                `json:"showProgress"`
	Steps        []handleEventRecord `json:"steps"`
}

var handleEventTitleApp = regexp.MustCompile(`^[^ ]+ - `)

func handleEventText(c tui.Component) string {
	lines := c.Render(100)
	for i, line := range lines {
		lines[i] = strings.TrimRight(widthx.StripAnsi(line), " ")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// TestHandleEventProbe prints the trace the handle-event scenario compares with the pinned Pi.
func TestHandleEventProbe(t *testing.T) {
	raw, err := os.ReadFile("../../test/parity/testdata/handle-event-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Sequences []struct {
			Name     string            `json:"name"`
			Embedded bool              `json:"embedded"`
			Expanded bool              `json:"expanded"`
			Steps    []handleEventStep `json:"steps"`
		} `json:"sequences"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	traces := []handleEventTrace{}
	for _, sequence := range corpus.Sequences {
		for _, showProgress := range []bool{true, false} {
			t.Run(sequence.Name, func(t *testing.T) {
				p := newEventProbe(t, showProgress)
				p.editor.EmbedWorkingStatus = sequence.Embedded
				p.toolsExpanded = sequence.Expanded
				p.editor.ThinkingLevel = "off"
				p.opts.CWD = "/work/project"
				p.keybindings = DefaultKeybindingsManager()
				p.isIdle = false
				ctx := t.Context()
				p.runCtx = ctx
				p.abortCtx, p.abortFn = context.WithCancel(ctx)
				handle := &recordingCompactHandle{}
				p.opts.SessionHandle = handle
				p.newRunner = inproc.NewRunner([]extension.Extension{{EntryRenderers: map[string]extension.EntryRenderer{
					"note": func(entry extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
						return &toggleEntryComponent{content: entry.Data.(string), expanded: options.Expanded}
					},
				}}}, t.TempDir())
				var titles []string
				previousTitle := setTerminalTitle
				setTerminalTitle = func(title string) { titles = append(titles, title) }
				t.Cleanup(func() { setTerminalTitle = previousTitle })

				trace := handleEventTrace{Name: sequence.Name, ShowProgress: showProgress, Steps: []handleEventRecord{}}
				previousChat := ""
				for _, step := range sequence.Steps {
					*p.progress = (*p.progress)[:0]
					titles = titles[:0]
					p.requestExit.Store(false)
					record := handleEventRecord{Step: step.Name}
					switch {
					case step.Event != nil:
						p.handleAgentEvent(step.Event.agentEvent(t))
					case step.Op == "esc":
						retries, compactions, aborted := handle.abortRetryCount, handle.abortCompactionCount, p.abortCtx
						if err := p.dispatchKey(ctx, "\x1b"); err != nil {
							t.Fatal(err)
						}
						switch {
						case handle.abortRetryCount > retries:
							record.Esc = "retry"
						case handle.abortCompactionCount > compactions:
							record.Esc = "compaction"
						case aborted.Err() != nil:
							record.Esc = "main"
						default:
							record.Esc = "none"
						}
					case step.Op == "queue":
						p.agent.ClearAllQueues()
						for _, text := range step.Steering {
							p.agent.Steer(userMsg(text))
						}
						for _, text := range step.FollowUp {
							p.agent.FollowUp(userMsg(text))
						}
					case step.Op == "thinking":
						p.agent.SetThinkingLevel(ai.ModelThinkingLevel(step.Level))
					case step.Op == "shutdownRequested":
						p.shutdownRequested.Store(true)
					case step.Op == "workingVisible":
						p.workingVisible = step.Visible
					default:
						t.Fatalf("unknown corpus step %q", step.Name)
					}
					record.Progress = append([]bool{}, *p.progress...)
					if p.activeStatusIndicator != nil {
						record.Status = p.activeStatusIndicator.Kind + "|" + p.activeStatusIndicator.Message
					}
					record.Pending = handleEventText(p.pendingMessagesContainer)
					record.Border = p.editor.ApplyBorderColor("x")
					record.Titles = []string{}
					for _, title := range titles {
						record.Titles = append(record.Titles, handleEventTitleApp.ReplaceAllString(title, ""))
					}
					record.Shutdown = p.requestExit.Load()
					record.Chat = "="
					if chat := handleEventText(p.chatContainer); chat != previousChat {
						record.Chat, previousChat = chat, chat
					}
					trace.Steps = append(trace.Steps, record)
				}
				traces = append(traces, trace)
			})
		}
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(traces); err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.WriteString("HANDLE_EVENT " + out.String())
}
