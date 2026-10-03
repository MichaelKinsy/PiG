package durableagent

// Ports packages/coding-agent/src/experimental/durable/subagent.ts

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

const subagentDeclaration = `{"name":"subagent","description":"Delegate a self-contained task to a subagent with the same tools and get its answer back. Give it everything it needs to know; it does not see this conversation.","parameters":{"type":"object","required":["task"],"properties":{"task":{"type":"string","description":"What the subagent should do"}}}}`

// subagentInput is the arguments of the subagent tool.
type subagentInput struct {
	Task string `json:"task"`
}

// Subagent is a foreground subagent: each call creates a child conversation owned by the call's task, runs the task there, and returns the child's answer. Aborting the call aborts the child. The child outlives the call, so the user can switch to it and keep talking.
var Subagent = createSubagent()

func createSubagent() *durable.Extension {
	var extension *durable.Extension
	execute := func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		input, err := durable.FromJsonValue[subagentInput](args)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		taskId := api.TaskId()
		created, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
			existing, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &taskId}, 1, nil)
			if err != nil {
				return nil, err
			}
			if len(existing.Items) > 0 {
				return existing.Items[0].Id, nil
			}
			// Starts as a copy of this conversation's agent; without this extension it cannot delegate further.
			child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: taskId}})
			if err != nil {
				return nil, err
			}
			return child.Id, harness.Configure(tx, child.Id, harness.AgentChange{Extensions: harness.SetTo(harness.ExtensionChange{Remove: []*durable.Extension{extension}})})
		})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		child := created.(durable.ConversationId)
		details := map[string]any{"conversationId": float64(child)}
		if err := api.Details(ctx, details); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		handle, err := api.Conversation(ctx, child)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if handle == nil {
			return durable.ToolExecutionResult{}, fmt.Errorf("Subagent %d does not exist", child)
		}
		// A rerun after a crash finds the child it created and the submission it made.
		submission, err := handle.Submit(ctx, durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(input.Task), RequestId: new("subagent:" + strconv.FormatInt(int64(taskId), 10))})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		settled, err := submission.Wait(ctx)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput || settled.Answer == nil {
			return durable.ToolExecutionResult{}, fmt.Errorf("Subagent %d failed: %s", child, settled.Status)
		}
		text, err := answerText(ctx, api, *settled.Answer)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: details, HasDetails: true}, nil
	}
	extension = new(durable.Extension{
		Name: "subagent",
		Tools: []*durable.ToolRegistration{new(durable.ToolRegistration{
			ToolSchema: subagentSchema(),
			Replay:     durable.ReplaySafe,
			Execute:    execute,
		})},
	})
	return extension
}

func subagentSchema() ai.ToolSchema {
	var schema ai.ToolSchema
	if err := json.Unmarshal([]byte(subagentDeclaration), &schema); err != nil {
		panic(err)
	}
	return schema
}

// answerText is the text of the answer entry: the text blocks of its assistant message joined, empty when the entry or message is absent.
func answerText(ctx context.Context, api durable.ToolExecutionApi, answer durable.EntryId) (string, error) {
	value, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
		entry, err := durable.TxEntry(tx, durable.AssistantEntry, answer)
		if err != nil || entry == nil {
			return (*durable.EntryRecord)(nil), err
		}
		return &entry.EntryRecord, nil
	})
	if err != nil {
		return "", err
	}
	entry, _ := value.(*durable.EntryRecord)
	if entry == nil || len(entry.Model) == 0 {
		return "", nil
	}
	message, ok := entry.Model[0].(ai.AssistantMessage)
	if !ok {
		return "", nil
	}
	var text strings.Builder
	for _, content := range message.Content {
		if block, ok := content.(ai.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	return text.String(), nil
}
