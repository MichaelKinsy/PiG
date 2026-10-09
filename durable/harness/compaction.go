// Ports packages/durable/src/harness/compaction.ts.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// CompactionInput is the input of the built-in compaction task.
type CompactionInput struct {
	Reason       durable.CompactionReason `json:"reason"`
	Instructions *string                  `json:"instructions,omitempty"`
}

// SummaryRequest is the pinned summarization request.
type SummaryRequest struct {
	Attempt       int                               `json:"attempt"`
	Model         durable.ModelRef                  `json:"model"`
	ThinkingLevel ai.ModelThinkingLevel             `json:"thinkingLevel"`
	StreamOptions durable.ConversationStreamOptions `json:"streamOptions"`
	MaxTokens     int                               `json:"maxTokens"`
	// Tail is the newest entry of the context the range was selected from.
	Tail durable.EntryId `json:"tail"`
	// FirstKept is the first entry kept verbatim; the summary's head.
	FirstKept durable.EntryId `json:"firstKept"`
}

// CompactionCheckpoint is the durable checkpoint of the compaction task: select, summarize (with the request), or
// retry (with the request and Until).
type CompactionCheckpoint struct {
	Phase string `json:"phase"`
	*SummaryRequest
	Until *float64 `json:"until,omitempty"`
}

// MarshalJSON writes the select phase without request members.
func (checkpoint CompactionCheckpoint) MarshalJSON() ([]byte, error) {
	if checkpoint.SummaryRequest == nil {
		return marshalPlain(struct {
			Phase string `json:"phase"`
		}{checkpoint.Phase})
	}
	type plain CompactionCheckpoint
	return marshalPlain(plain(checkpoint))
}

type compactionRuntime = durable.TaskRuntime[CompactionInput, CompactionCheckpoint, CompactionResult, *CompactionHooks]
type compactionTask = durable.RunningTask[CompactionInput, CompactionCheckpoint, CompactionResult]
type compactionNext = durable.NextTaskState[CompactionCheckpoint, CompactionResult]

// toolResultMaxChars is the longest tool result text a serialized summary source keeps.
const toolResultMaxChars = 2000

const (
	summaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	summarySuffix = "\n</summary>"
)

const summarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`

const summarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work. If the conversation starts with an earlier summary, preserve its information and fold the newer messages into it.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const (
	compactionSelect    = "select"
	compactionSummarize = "summarize"
	compactionRetry     = "retry"
)

// CompactionTask is the built-in compaction task (spec §8.7, compaction.ts:96-231): it selects an old prefix of the
// model context, summarizes it, and places a summary entry whose head is the first kept entry. A compaction the
// generation owns blocks it and appends directly; a conversation-owned one places its summary through a write
// submission. Its handlers admit submissions, which start generations, so the definition is assigned in init.
var CompactionTask durable.Task[CompactionInput, CompactionCheckpoint, CompactionResult, *CompactionHooks]

func init() {
	CompactionTask = durable.DefineTask(durable.TaskDefinition[CompactionInput, CompactionCheckpoint, CompactionResult, *CompactionHooks]{
		Name:    compactionTaskKind,
		Version: 1,
		Initial: func(CompactionInput) CompactionCheckpoint { return CompactionCheckpoint{Phase: compactionSelect} },
		Phases: map[string]durable.PhaseHandler[CompactionInput, CompactionCheckpoint, CompactionResult, *CompactionHooks]{
			compactionSelect:    compactionSelectHandler,
			compactionSummarize: compactionSummarizeHandler,
			compactionRetry:     compactionRetryHandler,
		},
		Abort: func(ctx context.Context, _ compactionTask, runtime compactionRuntime) error {
			return runtime.Commit(ctx, func(tx durable.Tx, _ compactionTask) (*compactionNext, error) {
				live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
				if err != nil {
					return nil, err
				}
				if err := RemoveCompactionStatus(live, runtime.TaskId()); err != nil {
					return nil, err
				}
				return abortedState[CompactionCheckpoint, CompactionResult](), nil
			})
		},
	})
}

func compactionSelectHandler(ctx context.Context, task compactionTask, runtime compactionRuntime) error {
	conversationId := runtime.ConversationId()
	agent, err := runtime.Agent(ctx)
	if err != nil {
		return err
	}
	settings := runtime.Settings()
	ref := agent.Model
	var model *ai.Model
	if ref != nil {
		model = runtime.Models().GetModel(ref.Provider, ref.ModelId)
	}
	if ref == nil || model == nil {
		return failCompactionNoModel(ctx, runtime, ref)
	}
	policy := settings.Compaction
	view, err := runtime.Context(ctx, conversationId, nil)
	if err != nil {
		return err
	}
	cut, ok := SelectCut(view, policy.KeepRecentTokens)
	if !ok {
		return completeCompaction(ctx, runtime)
	}
	firstKept := view.Entries[cut].Id
	request := CompactionRequest{
		Reason:       task.Input.Reason,
		Entries:      append([]durable.EntryRecord{}, view.Entries[:cut]...),
		Messages:     SummarizedMessages(view, cut),
		FirstKept:    firstKept,
		Instructions: task.Input.Instructions,
	}
	var decision *CompactionDecision
	if err := runtime.Hooks().Each("beforeCompact", func(hooks *CompactionHooks) error {
		if decision != nil || hooks == nil || hooks.BeforeCompact == nil {
			return nil
		}
		result, err := hooks.BeforeCompact(ctx, request, runtime)
		if err != nil {
			return err
		}
		decision = result
		return nil
	}); err != nil {
		return err
	}
	if decision != nil && decision.Decline {
		return completeCompaction(ctx, runtime)
	}
	if decision != nil && decision.Summary != nil {
		return placeHookSummary(ctx, runtime, firstKept, *decision.Summary)
	}
	maxTokens := math.Floor(0.8 * float64(policy.ReserveTokens))
	if model.Capabilities.MaxOutputTokens > 0 {
		maxTokens = math.Min(maxTokens, float64(model.Capabilities.MaxOutputTokens))
	}
	tail := firstKept
	for _, entry := range view.Entries {
		if entry.Id > tail {
			tail = entry.Id
		}
	}
	summary := SummaryRequest{
		Attempt:       1,
		Model:         *ref,
		ThinkingLevel: agent.ThinkingLevel,
		StreamOptions: settings.Stream,
		MaxTokens:     int(maxTokens),
		Tail:          tail,
		FirstKept:     firstKept,
	}
	return runtime.Commit(ctx, func(durable.Tx, compactionTask) (*compactionNext, error) {
		return runningState[CompactionCheckpoint, CompactionResult](CompactionCheckpoint{Phase: compactionSummarize, SummaryRequest: &summary}), nil
	})
}

func compactionSummarizeHandler(ctx context.Context, task compactionTask, runtime compactionRuntime) error {
	request := *task.State.Checkpoint.SummaryRequest
	ref := request.Model
	model := runtime.Models().GetModel(ref.Provider, ref.ModelId)
	if model == nil {
		return failCompactionNoModel(ctx, runtime, &ref)
	}
	// The context at tail is immutable, so this is the range select chose.
	tail := request.Tail
	view, err := runtime.Context(ctx, runtime.ConversationId(), &durable.ContextOptions{At: &tail})
	if err != nil {
		return err
	}
	cut := -1
	for index, entry := range view.Entries {
		if entry.Id == request.FirstKept {
			cut = index
			break
		}
	}
	now := int64(runtime.Now())
	messages := []ai.Message{
		ai.SystemMessage{Content: ai.SystemText(summarizationSystemPrompt), Timestamp: now},
		ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: summaryPrompt(SummarizedMessages(view, cut), task.Input.Instructions)}}, Timestamp: now},
	}
	forwarded := request.StreamOptions
	forwarded.Deferred = nil
	options := streamOptionsOf(forwarded, runtime.Signal(), request.ThinkingLevel)
	options.CacheRetention = ai.CacheRetention("none")
	options.MaxTokens = request.MaxTokens
	if options.SessionID, err = ensureProviderSessionId(ctx, runtime); err != nil {
		return err
	}
	result := runtime.Models().CompleteSimple(runtime.Signal(), model, ai.Context{Messages: messages}, options)
	// An abort mark or close: the abort invocation or the reopened task handles the committed state.
	if runtime.Signal().Err() != nil {
		return context.Cause(runtime.Signal())
	}
	if result == nil {
		return fmt.Errorf("summarization returned no message")
	}
	message := *result
	summary, hasSummary := summaryText(message)
	policy := runtime.Settings().Retry
	retry := message.StopReason == ai.StopReasonError && ai.IsRetryableAssistantError(message) && policy.Enabled && request.Attempt <= policy.MaxRetries
	until := 0.0
	if retry {
		until = runtime.Now() + float64(ai.RetryDelayMs(ai.RetryPolicy{BaseDelayMs: policy.BaseDelayMs, MaxAgentDelayMs: policy.MaxAgentDelayMs}, request.Attempt))
	}
	return runtime.Commit(ctx, func(tx durable.Tx, current compactionTask) (*compactionNext, error) {
		if err := RecordUsage(tx, runtime.ConversationId(), UsageModels, message.Provider+"/"+message.Model, message.Usage); err != nil {
			return nil, err
		}
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		if hasSummary {
			return placeSummary(tx, runtime, current, live, request.FirstKept, summary)
		}
		if retry {
			if status := FindCompactionStatus(live, runtime.TaskId()); status != nil {
				if err := status.Set("retry", delta.JsonObjectOf("at", until, "error", message.ErrorMessage)); err != nil {
					return nil, err
				}
			}
			next := CompactionCheckpoint{Phase: compactionRetry, SummaryRequest: &request, Until: &until}
			return runningState[CompactionCheckpoint, CompactionResult](next), nil
		}
		if err := RemoveCompactionStatus(live, runtime.TaskId()); err != nil {
			return nil, err
		}
		return failedState[CompactionCheckpoint, CompactionResult](summaryFailure(message), modelErrorReason), nil
	})
}

func compactionRetryHandler(ctx context.Context, task compactionTask, runtime compactionRuntime) error {
	checkpoint := *task.State.Checkpoint
	if err := runtime.Sleep(ctx, *checkpoint.Until); err != nil {
		return err
	}
	request := *checkpoint.SummaryRequest
	request.Attempt++
	return runtime.Commit(ctx, func(tx durable.Tx, _ compactionTask) (*compactionNext, error) {
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		if status := FindCompactionStatus(live, runtime.TaskId()); status != nil {
			if err := status.Set("attempt", float64(request.Attempt)); err != nil {
				return nil, err
			}
			status.Delete("retry")
		}
		return runningState[CompactionCheckpoint, CompactionResult](CompactionCheckpoint{Phase: compactionSummarize, SummaryRequest: &request}), nil
	})
}

// CreateCompaction creates a compaction task with its status in this commit (compaction.ts:237-251). owner is the
// generation that waits for it (a blocking compaction); without one it is conversation-owned, and background unless it
// is manual.
func CreateCompaction(tx durable.Tx, conversationId durable.ConversationId, input CompactionInput, owner *durable.TaskId) (durable.TaskId, error) {
	id := conversationId
	options := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &id}
	if owner != nil {
		options.Ownership = durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: *owner}
	}
	options.Background = owner == nil && input.Reason != durable.CompactionManual
	taskId, err := durable.CreateTask(tx, CompactionTask, input, options)
	if err != nil {
		return 0, err
	}
	live, err := docDraft(tx, LiveDoc, conversationId)
	if err != nil {
		return 0, err
	}
	status := CompactionStatus{TaskId: taskId, Reason: input.Reason, Blocking: owner != nil, Attempt: 1}
	return taskId, AddCompactionStatus(live, status)
}

// SelectCut returns the index in view.Entries of the first entry a summary keeps; ok is false when there is nothing to
// compact (spec §8.7, compaction.ts:259-277). It walks back from the tail until keepRecentTokens are kept, then cuts at
// the first candidate at or after that entry: an entry whose contribution starts with a user or assistant message,
// never a tool result, and never a user entry that a result of the preceding assistant's calls still follows.
func SelectCut(view durable.ContextView, keepRecentTokens int) (int, bool) {
	contributions := view.Contributions
	start := 0
	if view.Head != nil {
		start = 1
	}
	var candidates []int
	for index := start; index < len(contributions); index++ {
		if isCutCandidate(contributions, index) {
			candidates = append(candidates, index)
		}
	}
	kept := 0
	cut := -1
	for index := len(contributions) - 1; index >= start; index-- {
		for _, message := range contributions[index] {
			kept += ai.EstimateMessageTokens(message)
		}
		if kept < keepRecentTokens {
			continue
		}
		for _, candidate := range candidates {
			if candidate >= index {
				cut = candidate
				break
			}
		}
		if cut < 0 && len(candidates) > 0 {
			cut = candidates[len(candidates)-1]
		}
		break
	}
	if cut < 0 {
		return 0, false
	}
	for index := start; index < cut; index++ {
		if len(contributions[index]) > 0 {
			return cut, true
		}
	}
	return 0, false
}

func isCutCandidate(contributions [][]ai.Message, index int) bool {
	if len(contributions[index]) == 0 {
		return false
	}
	switch contributions[index][0].(type) {
	case ai.AssistantMessage:
		return true
	case ai.UserMessage:
	default:
		return false
	}
	// A result of the preceding assistant's calls that follows this entry, before the next assistant, belongs before
	// it.
	calls := map[string]bool{}
	for before := index - 1; before >= 0; before-- {
		var assistant *ai.AssistantMessage
		for _, v := range slices.Backward(contributions[before]) {
			if message, ok := v.(ai.AssistantMessage); ok {
				assistant = &message
				break
			}
		}
		if assistant == nil {
			continue
		}
		for _, call := range toolCallsOf(*assistant) {
			calls[call.ID] = true
		}
		break
	}
	if len(calls) == 0 {
		return true
	}
	for after := index; after < len(contributions); after++ {
		for position, message := range contributions[after] {
			switch typed := message.(type) {
			case ai.AssistantMessage:
				if after > index || position > 0 {
					return true
				}
			case ai.ToolResultMessage:
				if calls[typed.ToolCallID] {
					return false
				}
			}
		}
	}
	return true
}

// SummarizedMessages returns the model messages of the entries before cut: the head marker first, ordered like model
// context (spec §2.1).
func SummarizedMessages(view durable.ContextView, cut int) []ai.Message {
	var messages []ai.Message
	if cut < 0 {
		cut = max(len(view.Contributions)+cut, 0)
	}
	for _, contribution := range view.Contributions[:min(cut, len(view.Contributions))] {
		messages = append(messages, contribution...)
	}
	return OrderToolResults(messages)
}

// EstimateContext returns the size of a request over view followed by extra (spec §8.3, compaction.ts:325-341): the
// usage of the newest assistant appended after the head marker, whose request included the marker, plus estimates of
// the messages after it; without one, estimates of every message.
func EstimateContext(view durable.ContextView, extra []ai.Message) int {
	var measured *ai.AssistantMessage
	after := durable.EntryId(math.MinInt64)
	if view.Head != nil {
		after = view.Head.Id
	}
	for index := len(view.Entries) - 1; index >= 0 && measured == nil; index-- {
		if view.Entries[index].Id <= after {
			continue
		}
		contribution := view.Contributions[index]
		for _, c := range slices.Backward(contribution) {
			if message, ok := c.(ai.AssistantMessage); ok && ai.CalculateContextTokens(message.Usage) > 0 {
				measured = &message
				break
			}
		}
	}
	from, tokens := 0, 0
	if measured != nil {
		tokens = ai.CalculateContextTokens(measured.Usage)
		for index, v := range slices.Backward(view.Messages) {
			if sameAssistant(v, *measured) {
				from = index + 1
				break
			}
		}
	}
	for _, message := range view.Messages[from:] {
		tokens += ai.EstimateMessageTokens(message)
	}
	for _, message := range extra {
		tokens += ai.EstimateMessageTokens(message)
	}
	return tokens
}

// sameAssistant stands in for upstream's identity lastIndexOf: context derivation shares the contribution's message
// object with the model context, and two assistant messages with equal JSON are interchangeable here.
func sameAssistant(message ai.Message, measured ai.AssistantMessage) bool {
	candidate, ok := message.(ai.AssistantMessage)
	if !ok {
		return false
	}
	left, errLeft := json.Marshal(candidate)
	right, errRight := json.Marshal(measured)
	return errLeft == nil && errRight == nil && bytes.Equal(left, right)
}

// summaryText returns the summary of a clean stop with text and no tool call; anything else is not a summary.
func summaryText(message ai.AssistantMessage) (string, bool) {
	if message.StopReason != ai.StopReasonStop || len(toolCallsOf(message)) > 0 {
		return "", false
	}
	var texts []string
	for _, content := range message.Content {
		if text, ok := content.(ai.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	text := jsstring.Trim(strings.Join(texts, "\n"))
	return text, text != ""
}

func summaryFailure(message ai.AssistantMessage) string {
	if message.StopReason == ai.StopReasonError || message.StopReason == ai.StopReasonAborted {
		reason := message.ErrorMessage
		if reason == "" {
			reason = string(message.StopReason)
		}
		return "Summarization failed: " + reason
	}
	if message.StopReason == ai.StopReasonLength {
		return "Summarization hit the token limit; the summary is incomplete"
	}
	if len(toolCallsOf(message)) > 0 {
		return "Summarization attempted to call a tool"
	}
	return "Summarization produced no text"
}

// summaryPrompt is the summarizer's user message: the serialized conversation, the prompt, and any instructions.
func summaryPrompt(messages []ai.Message, instructions *string) string {
	focus := ""
	if instructions != nil {
		focus = "\n\nAdditional focus: " + *instructions
	}
	return "<conversation>\n" + SerializeConversation(messages) + "\n</conversation>\n\n" + summarizationPrompt + focus
}

// SerializeConversation writes messages as plain text, so the summarizer reads a transcript instead of continuing it.
// System messages are omitted (compaction.ts:369-397).
func SerializeConversation(messages []ai.Message) string {
	var parts []string
	for _, message := range messages {
		switch typed := message.(type) {
		case ai.UserMessage:
			if text := userContentText(typed.Content); text != "" {
				parts = append(parts, "[User]: "+text)
			}
		case ai.AssistantMessage:
			var thinking, texts, calls []string
			for _, content := range typed.Content {
				switch block := content.(type) {
				case ai.ThinkingContent:
					thinking = append(thinking, block.Thinking)
				case ai.TextContent:
					texts = append(texts, block.Text)
				case ai.ToolCall:
					calls = append(calls, block.Name+"("+callArguments(block)+")")
				}
			}
			if len(thinking) > 0 {
				parts = append(parts, "[Assistant thinking]: "+strings.Join(thinking, "\n"))
			}
			if len(texts) > 0 {
				parts = append(parts, "[Assistant]: "+strings.Join(texts, "\n"))
			}
			if len(calls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(calls, "; "))
			}
		case ai.ToolResultMessage:
			var texts []string
			for _, content := range typed.Content {
				if text, ok := content.(ai.TextContent); ok {
					texts = append(texts, text.Text)
				}
			}
			if text := strings.Join(texts, "\n"); text != "" {
				parts = append(parts, "[Tool result]: "+truncateUTF16(text, toolResultMaxChars))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func userContentText(content ai.UserContent) string {
	switch typed := content.(type) {
	case ai.UserText:
		return string(typed)
	case ai.UserContentBlocks:
		var texts []string
		for _, block := range typed {
			if text, ok := block.(ai.TextContent); ok {
				texts = append(texts, text.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

// callArguments renders `key=JSON.stringify(value)` pairs in the member order the model sent.
func callArguments(call ai.ToolCall) string {
	encoded, err := call.ArgumentsJSON()
	if err != nil {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ""
	}
	var pairs []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return strings.Join(pairs, ", ")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return strings.Join(pairs, ", ")
		}
		pairs = append(pairs, fmt.Sprint(key)+"="+jsStringEscapes(value))
	}
	return strings.Join(pairs, ", ")
}

// jsStringEscapes undoes the escapes Go's encoder adds and JSON.stringify does not: <, >, &, U+2028, and U+2029.
func jsStringEscapes(encoded []byte) string {
	var out strings.Builder
	for index := 0; index < len(encoded); index++ {
		if encoded[index] != '\\' || index+1 >= len(encoded) {
			out.WriteByte(encoded[index])
			continue
		}
		if encoded[index+1] == 'u' && index+6 <= len(encoded) {
			switch string(encoded[index+2 : index+6]) {
			case "003c":
				out.WriteByte('<')
				index += 5
				continue
			case "003e":
				out.WriteByte('>')
				index += 5
				continue
			case "0026":
				out.WriteByte('&')
				index += 5
				continue
			case "2028":
				out.WriteString("\u2028")
				index += 5
				continue
			case "2029":
				out.WriteString("\u2029")
				index += 5
				continue
			}
		}
		out.Write(encoded[index : index+2])
		index++
	}
	return out.String()
}

// truncateUTF16 keeps the first maxChars UTF-16 units of text, as JavaScript's slice does.
func truncateUTF16(text string, maxChars int) string {
	units := utf16.Encode([]rune(text))
	if len(units) <= maxChars {
		return text
	}
	return string(utf16.Decode(units[:maxChars])) + fmt.Sprintf("\n\n[... %d more characters truncated]", len(units)-maxChars)
}

// placeHookSummary places a summary supplied by a hook in its own commit.
func placeHookSummary(ctx context.Context, runtime compactionRuntime, firstKept durable.EntryId, summary string) error {
	return runtime.Commit(ctx, func(tx durable.Tx, current compactionTask) (*compactionNext, error) {
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		return placeSummary(tx, runtime, current, live, firstKept, summary)
	})
}

// placeSummary places the summary entry and completes (spec §8.7, compaction.ts:425-452). A blocking compaction,
// owned by its generation, appends it: the generation holds the run and waits. A conversation-owned one admits it as a
// write submission, placed at once when idle, otherwise at the next boundary, or settled stale. Nothing else may
// append to a busy conversation, so every non-blocking summary goes through admission.
func placeSummary(tx durable.Tx, runtime compactionRuntime, current compactionTask, live *delta.Object, firstKept durable.EntryId, summary string) (*compactionNext, error) {
	if err := RemoveCompactionStatus(live, runtime.TaskId()); err != nil {
		return nil, err
	}
	head := firstKept
	text := summaryPrefix + summary + summarySuffix
	data, err := durable.ToJsonValue(durable.CompactionEntryData{Reason: current.Input.Reason})
	if err != nil {
		return nil, err
	}
	entry := durable.EntryDraft{
		Kind:  durable.CompactionEntry.Kind,
		Head:  &head,
		Model: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: int64(runtime.Now())}},
		Data:  data,
	}
	var result CompactionResult
	if current.Owner == nil {
		requestId := fmt.Sprintf("compaction:%d", runtime.TaskId())
		draft := durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, RequestId: &requestId, Entry: &entry}
		id, err := AdmitSubmission(tx, runtime.ConversationId(), draft, runtime.Now(), QueueModesOf(runtime.Settings()))
		if err != nil {
			return nil, err
		}
		result.SubmissionId = &id
	} else {
		appended, err := tx.AppendEntry(runtime.ConversationId(), entry)
		if err != nil {
			return nil, err
		}
		id := appended.Id
		result.EntryId = &id
	}
	return completedState[CompactionCheckpoint, CompactionResult](result), nil
}

// completeCompaction removes the status and completes without a summary.
func completeCompaction(ctx context.Context, runtime compactionRuntime) error {
	return runtime.Commit(ctx, func(tx durable.Tx, _ compactionTask) (*compactionNext, error) {
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		if err := RemoveCompactionStatus(live, runtime.TaskId()); err != nil {
			return nil, err
		}
		return completedState[CompactionCheckpoint, CompactionResult](CompactionResult{}), nil
	})
}

func failCompactionNoModel(ctx context.Context, runtime compactionRuntime, ref *durable.ModelRef) error {
	message := noModelMessage(ref)
	return runtime.Commit(ctx, func(tx durable.Tx, _ compactionTask) (*compactionNext, error) {
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		if err := RemoveCompactionStatus(live, runtime.TaskId()); err != nil {
			return nil, err
		}
		return failedState[CompactionCheckpoint, CompactionResult](message, noModelReason), nil
	})
}
