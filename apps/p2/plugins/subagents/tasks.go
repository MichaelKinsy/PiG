package subagents

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/plugins/subagents/tools.ts (the tasks and the helpers its tools share). Persistent background subagents,
// adapted from pi-durable's test/examples/23-subagent-background.ts (MIT; see third-party/pi.LICENSE).

const subagentExtension = "subagent-tools"

type anchorState struct {
	Phase string `json:"phase"`
}

type anchorRuntime = durable.TaskRuntime[durable.JsonValue, anchorState, durable.JsonValue, any]
type anchorRunning = durable.RunningTask[durable.JsonValue, anchorState, durable.JsonValue]

func settleAnchor(ctx context.Context, runtime anchorRuntime, outcome durable.TaskOutcome[durable.JsonValue]) error {
	return runtime.Commit(ctx, func(durable.Tx, anchorRunning) (*durable.NextTaskState[anchorState, durable.JsonValue], error) {
		return &durable.NextTaskState[anchorState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &outcome}, nil
	})
}

// anchor owns the subagent conversation. Background and immediately terminal, so the main agent's Stop and idle waits do
// not reach the subagent; abort(background) still does.
var anchor = durable.DefineTask(durable.TaskDefinition[durable.JsonValue, anchorState, durable.JsonValue, any]{
	Name:    "p2.subagent-anchor",
	Version: 1,
	Initial: func(durable.JsonValue) anchorState { return anchorState{Phase: "done"} },
	Phases: map[string]durable.PhaseHandler[durable.JsonValue, anchorState, durable.JsonValue, any]{
		"done": func(ctx context.Context, _ anchorRunning, runtime anchorRuntime) error {
			return settleAnchor(ctx, runtime, durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted})
		},
	},
	Abort: func(ctx context.Context, _ anchorRunning, runtime anchorRuntime) error {
		return settleAnchor(ctx, runtime, durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted})
	},
})

// settledOrWithdrawn waits for submission to settle. A message queued behind the subagent's current run is placed when that
// run ends. If the run fails, Durable keeps it queued and nothing places it: the subagent goes idle with the message still
// queued, and the reporter would wait forever (keeping the worker busy). Withdraw it then; nil means withdrawn.
func settledOrWithdrawn(ctx context.Context, submission durable.Submission, subagent durable.ConversationHandle, pause func() error) (*durable.SettledSubmissionRecord, error) {
	type outcome struct {
		record durable.SettledSubmissionRecord
		err    error
	}
	settled := make(chan outcome, 1)
	waitCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		record, err := submission.Wait(waitCtx)
		settled <- outcome{record, err}
	}()
	for {
		idle := make(chan struct{})
		idleCtx, stopIdle := context.WithCancel(ctx)
		go func() {
			if subagent.WaitForIdle(idleCtx) == nil {
				close(idle)
			}
		}()
		select {
		case result := <-settled:
			stopIdle()
			if result.err != nil {
				return nil, result.err
			}
			return &result.record, nil
		case <-idle:
			stopIdle()
		case <-ctx.Done():
			stopIdle()
			return nil, ctx.Err()
		}
		status, err := submission.Status(ctx)
		if err != nil {
			return nil, err
		}
		if status.Status != durable.SubmissionQueued {
			result := <-settled
			return &result.record, result.err
		}
		// Idle while still queued: let an admission in flight settle, then withdraw.
		if err := pause(); err != nil {
			return nil, err
		}
		if status, err = submission.Status(ctx); err != nil {
			return nil, err
		}
		if status.Status != durable.SubmissionQueued {
			result := <-settled
			return &result.record, result.err
		}
		aborted, err := submission.Abort(ctx)
		if err != nil {
			return nil, err
		}
		if aborted == durable.SubmissionAborted {
			return nil, nil
		}
	}
}

const undelivered = "its previous run failed before this message was delivered; send it again"

func textOf(message *ai.AssistantMessage) string {
	if message == nil {
		return ""
	}
	var text strings.Builder
	for _, part := range message.Content {
		if part, ok := part.(ai.TextContent); ok {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

// agentMessageInput is a subagent's message as an input: the structural entry readers use, and what the model reads: the
// sender block, then the intent and the text. replyWith is the tool call that answers a question (the main agent's
// `subagent send`, a sibling's `team send`).
func agentMessageInput(message p2runtime.AgentMessage, replyWith string) (ai.UserContentBlocks, *durable.InputEntry) {
	label := map[p2runtime.AgentIntent]string{
		p2runtime.IntentAnswered: "answered, no reply needed",
		p2runtime.IntentFailed:   "failed",
		p2runtime.IntentQuestion: "asks; answer with " + replyWith,
		p2runtime.IntentNote:     "notes, no reply needed",
	}[message.Intent]
	data := delta.JsonObjectOf("from", delta.JsonObjectOf("name", message.FromName, "conversationId", message.FromConversationID), "intent", string(message.Intent), "text", message.Text)
	if message.Origin != nil {
		data.Set("origin", string(*message.Origin))
	}
	return ai.UserContentBlocks{
			ai.TextContent{Text: p2runtime.SenderText(p2runtime.SubagentSender{Name: message.FromName})},
			ai.TextContent{Text: "[" + label + "] " + message.Text},
		},
		&durable.InputEntry{Kind: p2runtime.AgentMessageEntry, Data: data}
}

// latestAnswer is the latest completed ("stop") answer with text of a conversation: a bounded, read-only, newest-first scan.
func latestAnswer(ctx context.Context, api durable.ToolExecutionApi, conversation durable.ConversationId) (string, bool, error) {
	found, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
		var cursor durable.Cursor
		for scanned := 0; scanned < 2000; {
			page, err := tx.ScanEntries(durable.EntryQuery{ConversationId: conversation}, 200, cursor)
			if err != nil {
				return nil, err
			}
			for i := range page.Items {
				scanned++
				if len(page.Items[i].Model) == 0 {
					continue
				}
				if message, ok := page.Items[i].Model[0].(ai.AssistantMessage); ok && message.StopReason == ai.StopReasonStop {
					if text := textOf(&message); strings.TrimSpace(text) != "" {
						return text, nil
					}
				}
			}
			if page.Next == nil {
				break
			}
			cursor = *page.Next
		}
		return nil, nil
	})
	if err != nil {
		return "", false, err
	}
	text, ok := found.(string)
	return text, ok, nil
}

// reporterInput is what a reporter delivers: to whom, and for whom to report.
type reporterInput struct {
	Name           string                 `json:"name"`
	ConversationId durable.ConversationId `json:"conversationId"`
	// Parent is the conversation that sent the message, which the report goes to: a `/tree` branch left while the subagent
	// works still gets its answer. Zero (checkpointed before it was recorded): the current main conversation.
	Parent   durable.ConversationId `json:"parent,omitempty"`
	Message  string                 `json:"message"`
	FollowUp bool                   `json:"followUp"`
	// Origin is "owner" when the owner started the main agent's turn: the subagent's turn inherits it.
	Origin string `json:"origin,omitempty"`
}

// report is what to tell the main agent, with the origin of the subagent's run that answered; a failure the reporter itself
// describes has the task's.
type report struct {
	Intent string `json:"intent"`
	Text   string `json:"text"`
	Origin string `json:"origin,omitempty"`
}

type reporterState struct {
	Phase  string  `json:"phase"`
	Report *report `json:"report,omitempty"`
}

type reporterRuntime = durable.TaskRuntime[reporterInput, reporterState, durable.JsonValue, any]
type reporterRunning = durable.RunningTask[reporterInput, reporterState, durable.JsonValue]
type reporterNext = durable.NextTaskState[reporterState, durable.JsonValue]

func terminalReporter(status durable.TaskOutcomeStatus) (*reporterNext, error) {
	return &reporterNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: status}}, nil
}

// pause waits ms on the Harness timer inside a phase; it ends early with ctx's error, as when the invocation ends.
func pause(ctx context.Context, runtime reporterRuntime, ms float64) error {
	fired := make(chan struct{})
	disarm := runtime.SetTimer(func() { close(fired) }, ms)
	select {
	case <-fired:
		return nil
	case <-ctx.Done():
		disarm()
		return ctx.Err()
	}
}

// reporter delivers one message, waits for the answer, then reports it to the conversation that sent the message as a
// follow-up input. Request IDs make both submissions exactly-once across restarts.
var reporter = durable.DefineTask(durable.TaskDefinition[reporterInput, reporterState, durable.JsonValue, any]{
	Name:    "p2.subagent-reporter",
	Version: 1,
	Initial: func(reporterInput) reporterState { return reporterState{Phase: "deliver"} },
	Phases: map[string]durable.PhaseHandler[reporterInput, reporterState, durable.JsonValue, any]{
		"deliver": func(ctx context.Context, task reporterRunning, runtime reporterRuntime) error {
			input := task.Input
			subagent, err := runtime.Conversation(ctx, input.ConversationId)
			if err != nil {
				return err
			}
			if subagent == nil {
				return fmt.Errorf("Subagent %s has no conversation", input.Name)
			}
			whenBusy := durable.WhenBusySteer
			if input.FollowUp {
				whenBusy = durable.WhenBusyFollowUp
			}
			draft := durable.InputSubmissionDraft{
				Type: durable.SubmissionTypeInput, Content: ai.UserText(input.Message), WhenBusy: whenBusy,
				RequestId: new(fmt.Sprintf("subagent:%d", task.Id)),
			}
			if input.Origin != "" {
				draft.InputEntry = &durable.InputEntry{Kind: p2runtime.SubagentTaskEntry, Data: delta.JsonObjectOf("origin", input.Origin)}
			}
			submission, err := subagent.Submit(ctx, draft)
			if err != nil {
				return err
			}
			settled, err := settledOrWithdrawn(ctx, submission, subagent, func() error { return pause(ctx, runtime, 1000) })
			if err != nil {
				return err
			}
			return runtime.Commit(ctx, func(tx durable.Tx, _ reporterRunning) (*reporterNext, error) {
				next := func(report *report) (*reporterNext, error) {
					return &reporterNext{Status: durable.TaskRunning, Checkpoint: &reporterState{Phase: "report", Report: report}}, nil
				}
				failed := func(text string) *report { return &report{Intent: "failed", Text: text, Origin: input.Origin} }
				if settled == nil {
					return next(failed(undelivered))
				}
				if settled.Status == durable.SubmissionUnanswered {
					reason := ""
					if settled.Reason != nil {
						reason = *settled.Reason
					}
					if reason == "aborted" {
						return next(nil)
					}
					return next(failed(reason))
				}
				// A continue settles like an input (it has no placed entry): its run's answer is reported the same way.
				if (settled.Type != durable.SubmissionTypeInput && settled.Type != durable.SubmissionTypeContinue) || settled.Answer == nil {
					return next(nil)
				}
				draftState, err := durable.TxDoc[contract.SubagentState](tx, contract.SubagentsDoc, durable.ROOT_CONVERSATION_ID)
				if err != nil {
					return nil, err
				}
				if !draftState.Object("agents").Has(input.Name) {
					return next(nil)
				}
				agent := draftState.Object("agents").Object(input.Name)
				reported, err := durable.FromJsonValue[[]durable.EntryId](agent.Array("reported").Snapshot())
				if err != nil {
					return nil, err
				}
				for _, id := range reported {
					if id == *settled.Answer {
						return next(nil)
					}
				}
				if _, err := agent.Array("reported").Push(float64(*settled.Answer)); err != nil {
					return nil, err
				}
				entry, err := tx.Entry(*settled.Answer)
				if err != nil {
					return nil, err
				}
				var answer *ai.AssistantMessage
				if entry != nil && len(entry.Model) > 0 {
					if message, ok := entry.Model[0].(ai.AssistantMessage); ok {
						answer = &message
					}
				}
				answered, err := p2runtime.AnswerOrigin(tx, input.ConversationId, *settled.Answer)
				if err != nil {
					return nil, err
				}
				return next(&report{Intent: "answered", Text: textOf(answer), Origin: string(answered)})
			})
		},
		"report": func(ctx context.Context, task reporterRunning, runtime reporterRuntime) error {
			if report := task.State.Checkpoint.Report; report != nil {
				input := task.Input
				parent := input.Parent
				if parent == 0 {
					var err error
					if parent, err = p2runtime.MainConversation(ctx, runtime); err != nil {
						return err
					}
				}
				target, err := runtime.Conversation(ctx, parent)
				if err != nil {
					return err
				}
				if target != nil {
					message := p2runtime.AgentMessage{
						FromName: input.Name, FromConversationID: float64(input.ConversationId),
						Intent: p2runtime.AgentIntent(report.Intent), Text: report.Text,
					}
					if report.Origin != "" {
						origin := p2runtime.ConversationOrigin(report.Origin)
						message.Origin = &origin
					}
					content, entry := agentMessageInput(message, "subagent send to "+input.Name)
					if _, err := target.Submit(ctx, durable.InputSubmissionDraft{
						Type: durable.SubmissionTypeInput, Content: content, InputEntry: entry, WhenBusy: durable.WhenBusyFollowUp,
						RequestId: new(fmt.Sprintf("subagent-report:%d", task.Id)),
					}); err != nil {
						return err
					}
				}
			}
			return runtime.Commit(ctx, func(durable.Tx, reporterRunning) (*reporterNext, error) {
				return &reporterNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted}}, nil
			})
		},
	},
	Abort: func(ctx context.Context, _ reporterRunning, runtime reporterRuntime) error {
		return runtime.Commit(ctx, func(durable.Tx, reporterRunning) (*reporterNext, error) {
			return terminalReporter(durable.OutcomeAborted)
		})
	},
})

