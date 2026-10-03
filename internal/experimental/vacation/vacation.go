// Package vacation is a durable vacation planning agent on the pi-durable Harness: the coding agent of internal/experimental/durableagent with the coding tools and pi's coding prompt replaced by a vacation planner.
package vacation

// Ports packages/coding-agent/src/experimental/vacation/vacation.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// search: slow, fake, and safe to rerun.

// searchResults are the canned results of one topic; each topic takes a different time, so a crash can land between them.
type searchResults struct {
	seconds int
	results []string
}

var searches = map[string]searchResults{
	"weather": {seconds: 6, results: []string{"Saturday: 24°C and sunny", "Sunday: 21°C, a short shower around 3 pm"}},
	"museums": {seconds: 10, results: []string{
		"Kunsthistorisches Museum: open 10-18, book a time slot",
		"Belvedere: Klimt's The Kiss, quietest before 11",
		"Albertina: Monet to Picasso, open until 21 on Friday",
	}},
	"trains": {seconds: 30, results: []string{"Railjet from Salzburg: 2h 22m, every 30 min", "Nightjet from Munich: arrives 06:20"}},
}

const searchDeclaration = `{"name":"search","description":"Search travel information about a city. Topics: weather, museums, trains. Slow; call several in parallel.","parameters":{"type":"object","required":["topic","city"],"properties":{"topic":{"anyOf":[{"type":"string","const":"weather"},{"type":"string","const":"museums"},{"type":"string","const":"trains"}]},"city":{"type":"string"}}}}`

const researchDeclaration = `{"name":"research","description":"Start a research subagent in the background. It searches while you keep talking with the user; its report arrives later as a message starting with [research report].","parameters":{"type":"object","required":["task"],"properties":{"task":{"type":"string","description":"What to research, with the city and dates"}}}}`

// searchStep is the time one source of a search takes.
var searchStep = 2 * time.Second

type searchInput struct {
	Topic string `json:"topic"`
	City  string `json:"city"`
}

func schemaOf(declaration string) ai.ToolSchema {
	var schema ai.ToolSchema
	if err := json.Unmarshal([]byte(declaration), &schema); err != nil {
		panic(err)
	}
	return schema
}

func executeSearch(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	input, err := durable.FromJsonValue[searchInput](args)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	found, ok := searches[input.Topic]
	if !ok {
		return durable.ToolExecutionResult{}, fmt.Errorf("Unknown search topic: %s", input.Topic)
	}
	steps := (found.seconds + 1) / 2
	for step := 1; step <= steps; step++ {
		api.Output("searching " + input.Topic + " in " + input.City + ": source " + strconv.Itoa(step) + "/" + strconv.Itoa(steps) + "\n")
		timer := time.NewTimer(searchStep)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return durable.ToolExecutionResult{}, context.Cause(ctx)
		}
	}
	for _, result := range found.results {
		api.Output("- " + result + "\n")
	}
	return durable.ToolExecutionResult{}, nil
}

// Search is what the research subagent gets: only search. A search only reads, so a call cut off by a crash simply runs again.
var Search = new(durable.Extension{
	Name: "vacation-search",
	Tools: []*durable.ToolRegistration{new(durable.ToolRegistration{
		ToolSchema: schemaOf(searchDeclaration),
		Replay:     durable.ReplaySafe,
		Execute:    executeSearch,
	})},
})

// research: a background subagent that reports back.

type researchInput struct {
	Task string `json:"task"`
}

// researchState is the checkpoint of the research task: deliver the task to the subagent, then report back.
type researchState struct {
	Phase  string `json:"phase"`
	Report string `json:"report,omitempty"`
}

type (
	researchRecord  = durable.RunningTask[researchInput, researchState, durable.JsonValue]
	researchRuntime = durable.TaskRuntime[researchInput, researchState, durable.JsonValue, any]
	researchNext    = durable.NextTaskState[researchState, durable.JsonValue]
)

func terminalOutcome(outcome durable.TaskOutcome[durable.JsonValue]) *researchNext {
	return &researchNext{Status: durable.TaskTerminal, Outcome: &outcome}
}

// researchReport is the report of a settled subagent submission: its answer, or the failure.
func researchReport(tx durable.Tx, settled durable.SettledSubmissionRecord) (string, error) {
	report := "[research report] The research failed: ?"
	if settled.Status == durable.SubmissionUnanswered {
		reason := ""
		if settled.Reason != nil {
			reason = *settled.Reason
		}
		report = "[research report] The research failed: " + reason
	}
	if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput || settled.Answer == nil {
		return report, nil
	}
	entry, err := durable.TxEntry(tx, durable.AssistantEntry, *settled.Answer)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	if entry != nil && len(entry.Model) > 0 {
		if message, ok := entry.Model[0].(ai.AssistantMessage); ok {
			for _, part := range message.Content {
				if block, ok := part.(ai.TextContent); ok {
					text.WriteString(block.Text)
				}
			}
		}
	}
	return "[research report] " + text.String(), nil
}

func deliver(ctx context.Context, task researchRecord, runtime researchRuntime) error {
	// The subagent's conversation is owned by this task. No Session call may run inside a commit.
	var owned *durable.ConversationId
	if err := runtime.Commit(ctx, func(tx durable.Tx, _ researchRecord) (*researchNext, error) {
		page, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &task.Id}, 1, nil)
		if err != nil {
			return nil, err
		}
		if len(page.Items) > 0 {
			owned = &page.Items[0].Id
		}
		return nil, nil
	}); err != nil {
		return err
	}
	if owned == nil {
		return errors.New("The research task owns no conversation")
	}
	child, err := runtime.Conversation(ctx, *owned)
	if err != nil {
		return err
	}
	if child == nil {
		return fmt.Errorf("Conversation %d does not exist", *owned)
	}
	// A rerun after a crash gets the same submission back.
	submission, err := child.Submit(ctx, durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(task.Input.Task), RequestId: new("research:" + strconv.FormatInt(int64(task.Id), 10))})
	if err != nil {
		return err
	}
	settled, err := submission.Wait(ctx)
	if err != nil {
		return err
	}
	return runtime.Commit(ctx, func(tx durable.Tx, _ researchRecord) (*researchNext, error) {
		report, err := researchReport(tx, settled)
		if err != nil {
			return nil, err
		}
		return &researchNext{Status: durable.TaskRunning, Checkpoint: &researchState{Phase: "report", Report: report}}, nil
	})
}

func report(ctx context.Context, task researchRecord, runtime researchRuntime) error {
	main, err := runtime.Conversation(ctx, runtime.ConversationId())
	if err != nil {
		return err
	}
	if main == nil {
		return fmt.Errorf("Conversation %d does not exist", runtime.ConversationId())
	}
	if _, err := main.Submit(ctx, durable.InputSubmissionDraft{
		Type: durable.SubmissionTypeInput, Content: ai.UserText(task.State.Checkpoint.Report), WhenBusy: durable.WhenBusyFollowUp,
		RequestId: new("research-report:" + strconv.FormatInt(int64(task.Id), 10)),
	}); err != nil {
		return err
	}
	return runtime.Commit(ctx, func(durable.Tx, researchRecord) (*researchNext, error) {
		var result durable.JsonValue
		return terminalOutcome(durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}), nil
	})
}

// Research delivers the task to its subagent and posts the report to the main conversation as a new message.
var Research = durable.DefineTask(durable.TaskDefinition[researchInput, researchState, durable.JsonValue, any]{
	Name:    "vacation.research",
	Version: 1,
	Initial: func(researchInput) researchState { return researchState{Phase: "deliver"} },
	Phases: map[string]durable.PhaseHandler[researchInput, researchState, durable.JsonValue, any]{
		"deliver": deliver,
		"report":  report,
	},
	Abort: func(ctx context.Context, _ researchRecord, runtime researchRuntime) error {
		return runtime.Commit(ctx, func(durable.Tx, researchRecord) (*researchNext, error) {
			return terminalOutcome(durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}), nil
		})
	},
})

func executeResearch(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	input, err := durable.FromJsonValue[researchInput](args)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	if _, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
		// Background: the main conversation stays free while the research runs.
		owner, err := durable.CreateTask(tx, Research, input, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, Background: true})
		if err != nil {
			return nil, err
		}
		child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
		if err != nil {
			return nil, err
		}
		return nil, harness.Configure(tx, child.Id, harness.AgentChange{
			Extensions:   harness.SetTo(harness.ExtensionChange{Exact: true, List: []*durable.Extension{Search}}),
			Instructions: harness.SetTo("You are a research subagent for a vacation planner. Search weather, museums, and trains in parallel, in one step, then report the findings in a few short bullet points."),
		})
	}); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Research started in the background."}}}, nil
}

// Vacation is what the main conversation gets: the prompt, research, and the task that delivers its report.
var Vacation = new(durable.Extension{
	Name:  "vacation",
	Tools: []*durable.ToolRegistration{new(durable.ToolRegistration{ToolSchema: schemaOf(researchDeclaration), Execute: executeResearch})},
	Tasks: []durable.AnyTask{Research},
	Sections: []*durable.PromptSection{harness.Section("preamble", func(context.Context, durable.PromptInput) (*string, error) {
		return new("You are a friendly vacation planning assistant. Keep answers short. Hand all research to the research tool; while it runs, keep chatting with the user. When a [research report] arrives, turn it into a short plan."), nil
	}, harness.SectionOptions{Tag: new(false)})},
})
