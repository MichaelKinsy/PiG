package pico3

import (
	"context"
	"maps"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

// CollapseHooks are the hook points pi.collapse calls.
type CollapseHooks struct {
	// BeforeCollapse may decline the collapse or supply instructions or a
	// ready summary; the first handler returning a decision wins.
	BeforeCollapse func(ctx context.Context, reason string, through Id, entries []Entry, info HookApi) (*CollapseDecision, error)
}

// CollapseDecision is a BeforeCollapse result: decline, or override the
// instructions and optionally supply the summary.
type CollapseDecision struct {
	Decline      bool
	Instructions *string
	Summary      *string
}

func collapseHooksOf(handlers any) *CollapseHooks {
	switch typed := handlers.(type) {
	case *CollapseHooks:
		return typed
	case CollapseHooks:
		return &typed
	default:
		return nil
	}
}

// collapseBase is carried unchanged through every collapse checkpoint except
// the attempt.
type collapseBase struct {
	ExpectedHead  *Id         `json:"expectedHead"`
	Instructions  *string     `json:"instructions,omitempty"`
	Model         ModelRef    `json:"model"`
	ThinkingLevel string      `json:"thinkingLevel"`
	Retry         RetryPolicy `json:"retry"`
	Attempt       int         `json:"attempt"`
}

func (base collapseBase) checkpoint(phase string, extra JsonObject) Checkpoint {
	checkpoint := storedObject(base)
	checkpoint["phase"] = phase
	maps.Copy(checkpoint, extra)
	return checkpoint
}

func collapseBaseOf(checkpoint Checkpoint) collapseBase {
	var base collapseBase
	_ = decodeInto(checkpoint, &base)
	return base
}

type collapseInput struct {
	Reason       string  `json:"reason"`
	Through      Id      `json:"through"`
	Instructions *string `json:"instructions,omitempty"`
}

func collapseInputOf(task Task) collapseInput {
	var input collapseInput
	_ = decodeInto(task.Input, &input)
	return input
}

func collapseFailed(reason, detail string) Completion {
	return Failed(JsonObject{"reason": reason, "detail": detail})
}

var collapseConfig = &KindConfig{
	Rewindable: []ConfigKey{
		{Key: "threshold", Default: 0.0},
		{Key: "keepRecent", Default: 20_000.0},
	},
}

// collapseKind summarizes the conversation through an entry and appends a
// pi.summary entry that becomes the new context head.
var collapseKind = &Kind{
	Name:     "pi.collapse",
	Config:   collapseConfig,
	Inflight: []string{"summarizing"},
	Initial:  collapseInitial,
	Phases: map[string]PhaseHandler{
		"summarizing": func(_ context.Context, task Task, rt *Runtime) (Step, error) {
			return collapseAfterFailure(collapseBaseOf(task.Checkpoint), nil, "interrupted", rt), nil
		},
		"retrying": func(ctx context.Context, task Task, rt *Runtime) (Step, error) {
			if err := rt.Sleep(ctx, numberOr(task.Checkpoint["untilMs"], 0)); err != nil {
				return Step{}, err
			}
			base := collapseBaseOf(task.Checkpoint)
			base.Attempt++
			return summarizeNow(ctx, task, base, rt, false)
		},
		"prepared": collapsePrepared,
	},
	Abort: func(context.Context, Task, *Runtime) (AbortClosure, error) {
		return func(context.Context, *Tx, Task) (JsonValue, error) { return nil, nil }, nil
	},
}

func collapseInitial(ctx context.Context, task Task, rt *Runtime) (Step, error) {
	state, err := rt.Rewindable(ctx, task.ConversationId)
	if err != nil {
		return Step{}, err
	}
	if _, ok := state["model"]; !ok || state["model"] == nil {
		return done(collapseFailed("no_model", "no model configured")), nil
	}
	sticky, err := rt.Sticky(ctx, task.ConversationId)
	if err != nil {
		return Step{}, err
	}
	head, err := rt.NewestEntry(ctx, task.ConversationId, NewestOptions{WithHead: true})
	if err != nil {
		return Step{}, err
	}
	input := collapseInputOf(task)
	view, err := rt.Context(ctx, task.ConversationId, &input.Through)
	if err != nil {
		return Step{}, err
	}
	decision, err := beforeCollapse(ctx, rt, input, view.Entries)
	if err != nil {
		return Step{}, err
	}
	if decision.Decline {
		return done(collapseFailed("declined", "declined by beforeCollapse")), nil
	}
	base := collapseBase{ExpectedHead: entryIdOf(head), ThinkingLevel: stringOr(state["thinkingLevel"], "off"), Attempt: 1}
	_ = decodeInto(state["model"], &base.Model)
	_ = decodeInto(sticky["retry"], &base.Retry)
	instructions := decision.Instructions
	if instructions == nil {
		instructions = input.Instructions
	}
	if instructions != nil && *instructions != "" {
		base.Instructions = instructions
	}
	if decision.Summary != nil {
		summary := *decision.Summary
		return Step{Build: func(_ context.Context, tx *Tx, current Task) (Transition, error) {
			moved, err := collapseHeadMoved(tx, current.ConversationId, base)
			if err != nil || moved {
				failure := collapseFailed("stale", "head moved during beforeCollapse")
				return Transition{Completion: &failure}, err
			}
			return Transition{Checkpoint: base.checkpoint("prepared", JsonObject{"summary": summary})}, nil
		}}, nil
	}
	return summarizeNow(ctx, task, base, rt, true)
}

func beforeCollapse(ctx context.Context, rt *Runtime, input collapseInput, entries []Entry) (CollapseDecision, error) {
	var decision CollapseDecision
	err := rt.Hooks.Each(ctx, func(handlers any, api HookApi) (any, error) {
		hooks := collapseHooksOf(handlers)
		if hooks == nil || hooks.BeforeCollapse == nil {
			return nil, nil
		}
		result, err := hooks.BeforeCollapse(ctx, input.Reason, input.Through, entries, api)
		if result == nil {
			return nil, err
		}
		return result, err
	}, func(value any) bool {
		decision = *value.(*CollapseDecision)
		return true
	})
	return decision, err
}

func collapsePrepared(_ context.Context, task Task, rt *Runtime) (Step, error) {
	summary := str(task.Checkpoint, "summary")
	base := collapseBaseOf(task.Checkpoint)
	through := collapseInputOf(task).Through
	return Step{Done: func(_ context.Context, tx *Tx, current Task) (Completion, error) {
		view, err := tx.Context(current.ConversationId, nil)
		if err != nil {
			return Completion{}, err
		}
		if !sameId(entryIdOf(view.Head), base.ExpectedHead) {
			return collapseFailed("stale", "head moved during collapse"), nil
		}
		head := SelfHead()
		for _, entry := range view.Entries {
			if entry.Id > through {
				head = HeadAt(entry.Id)
				break
			}
		}
		id, err := tx.AppendEntry(current.ConversationId, NewEntry{
			Kind:  "pi.summary",
			Data:  JsonObject{"through": float64(through)},
			Model: []JsonObject{userMessage(summary, rt.Now())},
			Head:  head,
		})
		if err != nil {
			return Completion{}, err
		}
		return Completed(JsonObject{"summary": float64(id)}), nil
	}}, nil
}

func sameId(left, right *Id) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func collapseHeadMoved(tx *Tx, conversationId Id, base collapseBase) (bool, error) {
	head, err := tx.NewestEntry(conversationId, NewestOptions{WithHead: true})
	if err != nil {
		return false, err
	}
	return !sameId(entryIdOf(head), base.ExpectedHead), nil
}

func summarizeNow(ctx context.Context, task Task, base collapseBase, rt *Runtime, checkHead bool) (Step, error) {
	stale, err := CommitAs(ctx, rt, func(_ context.Context, tx *Tx, current Task) (bool, error) {
		if checkHead {
			moved, err := collapseHeadMoved(tx, current.ConversationId, base)
			if err != nil || moved {
				return moved, err
			}
		}
		return false, tx.Checkpoint(base.checkpoint("summarizing", nil))
	})
	if err != nil {
		return Step{}, err
	}
	if stale {
		return done(collapseFailed("stale", "head moved during beforeCollapse")), nil
	}
	model := rt.Models.Resolve(base.Model)
	if model == nil {
		return done(collapseFailed("no_model", "model unavailable")), nil
	}
	through := collapseInputOf(task).Through
	view, err := rt.Context(ctx, task.ConversationId, &through)
	if err != nil {
		return Step{}, err
	}
	message, err := streamSummary(ctx, model, collapseRequest(view.Messages, base, rt.Now()), base.ThinkingLevel, rt)
	if err != nil {
		if ctx.Err() != nil {
			return Step{}, err
		}
		return collapseAfterFailure(base, nil, errorString(err), rt), nil
	}
	return classifySummary(base, message, rt), nil
}

func collapseRequest(messages []JsonObject, base collapseBase, now float64) []JsonObject {
	request := append([]JsonObject{}, messages...)
	if tools := EffectiveTools(messages); len(tools) > 0 {
		removed := make([]any, len(tools))
		for index, tool := range tools {
			removed[index] = tool
		}
		request = append(request, JsonObject{"role": "system", "content": "", "toolsRemoved": removed, "timestamp": now})
	}
	instructions := "Summarize the conversation so far for continuation."
	if base.Instructions != nil {
		instructions = *base.Instructions
	}
	return append(request, userMessage(instructions+"\n\nRespond with the summary only.", now))
}

// streamSummary consumes the stream up to its terminal event; a nil message
// means the stream ended without one.
func streamSummary(ctx context.Context, model *ai.Model, messages []JsonObject, thinkingLevel string, rt *Runtime) (*ai.AssistantMessage, error) {
	for event, err := range rt.Models.Stream(ctx, model, RequestOptions{Messages: messages, ThinkingLevel: thinkingLevel}) {
		if err != nil {
			return nil, err
		}
		switch terminal := event.(type) {
		case ai.DoneEvent:
			return terminal.Message, nil
		case ai.ErrorEvent:
			return terminal.Error, nil
		}
	}
	return nil, nil
}

func classifySummary(base collapseBase, message *ai.AssistantMessage, rt *Runtime) Step {
	if message == nil {
		return collapseAfterFailure(base, nil, "no message", rt)
	}
	var text strings.Builder
	for _, block := range message.Content {
		switch content := block.(type) {
		case ai.ToolCall:
			failed := *message
			failed.StopReason = ai.StopReasonError
			return collapseAfterFailure(base, &failed, "summarizer returned tool calls", rt)
		case ai.TextContent:
			text.WriteString(content.Text)
		}
	}
	if message.StopReason == ai.StopReasonError {
		detail := message.ErrorMessage
		if detail == "" {
			detail = "provider error"
		}
		return collapseAfterFailure(base, message, detail, rt)
	}
	return Step{Next: base.checkpoint("prepared", JsonObject{"summary": text.String()})}
}

func collapseAfterFailure(base collapseBase, message *ai.AssistantMessage, detail string, rt *Runtime) Step {
	decision := retryDecision(base.Retry, base.Attempt, message, rt.Now())
	if !decision.retry {
		return done(collapseFailed(decision.reason, detail))
	}
	return Step{Build: func(_ context.Context, tx *Tx, current Task) (Transition, error) {
		err := emit(tx, ViewEvent{"type": "compaction.retrying", "taskId": float64(current.Id), "attempt": base.Attempt, "retryAt": decision.untilMs, "error": detail})
		return Transition{Checkpoint: base.checkpoint("retrying", JsonObject{"untilMs": decision.untilMs, "lastError": detail})}, err
	}}
}

type collapseExchange struct {
	last   Id
	tokens int
}

// chooseThrough picks the newest exchange boundary whose later exchanges fit
// in keepRecent tokens; an assistant and its tool results form one exchange.
// It returns nil when everything fits.
func chooseThrough(entries []Entry, keepRecent float64) *Id {
	var exchanges []*collapseExchange
	var open *collapseExchange
	for _, entry := range entries {
		role := ""
		if len(entry.Model) > 0 {
			role = str(entry.Model[0], "role")
		}
		tokens := estimateTokens(entry.Model, false)
		switch {
		case role == "assistant":
			open = &collapseExchange{last: entry.Id, tokens: tokens}
			exchanges = append(exchanges, open)
		case role == "toolResult" && open != nil:
			open.last = entry.Id
			open.tokens += tokens
		default:
			open = nil
			exchanges = append(exchanges, &collapseExchange{last: entry.Id, tokens: tokens})
		}
	}
	retained := 0
	index := len(exchanges) - 1
	for index >= 0 && float64(retained+exchanges[index].tokens) <= keepRecent {
		retained += exchanges[index].tokens
		index--
	}
	if index < 0 {
		return nil
	}
	if index == len(exchanges)-1 {
		if index == 0 {
			return nil
		}
		return &exchanges[index-1].last
	}
	return &exchanges[index].last
}
