// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// subagentExtension is the foreground sub-agent tool of bench.mjs (W4e; example 22): the child conversation is owned by the
// tool call's task, and a rerun after a crash finds the child and the submission it already made.
func subagentExtension() *durable.Extension {
	var subagent *durable.Extension
	var schema ai.ToolSchema
	// Declared order matters (the reference's TypeBox schema is {type, required, properties}); see the lookup tool in main.go.
	if err := json.Unmarshal([]byte(`{"name":"subagent","description":"Delegate a self-contained task to a subagent and get its answer back.","parameters":{"type":"object","required":["task"],"properties":{"task":{"type":"string"}}}}`), &schema); err != nil {
		panic(err)
	}
	subagent = &durable.Extension{Name: "subagent", Tools: []*durable.ToolRegistration{{
		ToolSchema: schema,
		Replay:     durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			task, _ := args.(map[string]any)["task"].(string)
			created, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				taskID := api.TaskId()
				existing, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &taskID}, 1, nil)
				if err != nil {
					return nil, err
				}
				if len(existing.Items) > 0 {
					return existing.Items[0].Id, nil
				}
				child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: taskID}})
				if err != nil {
					return nil, err
				}
				return child.Id, harness.Configure(tx, child.Id, harness.AgentChange{Extensions: harness.SetTo(harness.ExtensionChange{Edit: &harness.ExtensionEdit{Remove: []*durable.Extension{subagent}}})})
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			child := created.(durable.ConversationId)
			if err := api.Details(ctx, delta.JsonObjectOf("conversationId", float64(child))); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			handle, err := api.Conversation(ctx, child)
			if err != nil || handle == nil {
				return durable.ToolExecutionResult{}, fmt.Errorf("child conversation: %w", err)
			}
			request := durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(task), RequestId: new(fmt.Sprintf("subagent:%d", api.TaskId()))}
			submission, err := handle.Submit(ctx, request)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			settled, err := submission.Wait(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
				return durable.ToolExecutionResult{}, fmt.Errorf("Subagent failed: %s", settled.Status)
			}
			entry, err := api.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.Entry(*settled.Answer) })
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			answer := entry.(*durable.EntryRecord)
			message, _ := answer.Model[0].(ai.AssistantMessage)
			var text strings.Builder
			for _, block := range message.Content {
				if t, ok := block.(ai.TextContent); ok {
					text.WriteString(t.Text)
				}
			}
			return durable.ToolExecutionResult{
				Output:     []ai.ToolResultMessageContent{ai.TextContent{Text: text.String()}},
				Details:    delta.JsonObjectOf("conversationId", float64(child)),
				HasDetails: true,
			}, nil
		},
	}}}
	return subagent
}
