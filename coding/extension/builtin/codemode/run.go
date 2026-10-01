package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	sandbox "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
)

func utf16Units(s string) []uint16 { return utf16.Encode([]rune(s)) }

func decodeUnits(units []uint16) string { return string(utf16.Decode(units)) }

// run is the state of one script: the nested call rows the result reports. Nested calls run in their own goroutines, so
// the rows are guarded.
type run struct {
	toolCallID string
	onUpdate   agent.ToolUpdateCallback
	tc         *extension.ToolContext
	options    Options

	mu    sync.Mutex
	calls []*NestedCall
	// modelUsage is the usage of the script's `models.*` calls.
	modelUsage *ai.Usage
}

func (r *run) snapshot() []NestedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]NestedCall, len(r.calls))
	for i, call := range r.calls {
		out[i] = *call
	}
	return out
}

func (r *run) publish() {
	if r.onUpdate != nil {
		r.onUpdate(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: ToolDetails{Calls: r.snapshot()}})
	}
}

func (r *run) update(record *NestedCall, change func(*NestedCall)) {
	r.mu.Lock()
	change(record)
	r.mu.Unlock()
	r.publish()
}

// finishCalls marks the calls still running as cancelled (the script ended, timed out or was aborted) and returns the rows.
func (r *run) finishCalls() []NestedCall {
	r.mu.Lock()
	for _, call := range r.calls {
		if call.Status == StatusRunning {
			call.Status = StatusCancelled
		}
	}
	r.mu.Unlock()
	return r.snapshot()
}

// nestedCall runs a nested tool through the agent loop's pipeline, so validation, hooks and permission checks apply as
// for direct calls.
func (r *run) nestedCall(tool extension.AgentTool) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(callCtx context.Context, args json.RawMessage) (json.RawMessage, error) {
		record := &NestedCall{ID: r.toolCallID + "/?", Name: tool.Name, Args: previewArgs(args), Status: StatusRunning}
		r.mu.Lock()
		r.calls = append(r.calls, record)
		r.mu.Unlock()
		r.publish()
		startedAt := time.Now()
		// The nested runner calls this once the call has its id and its place in call order; the script's next call waits for it.
		callCtx = extension.WithCallInitiation(callCtx, func() { sandbox.CallInitiated(callCtx) })
		outcome, err := r.tc.ExecuteTool(tool.Name, args, &extension.ExecuteToolOptions{Signal: callCtx})
		if err != nil {
			// upstream: a rejected ctx.executeTool leaves the row running without a duration, and the script rejects
			// with the error; the row becomes cancelled when the script ends (execute.ts executeCodemode).
			return nil, err
		}
		duration := millisSince(startedAt)
		r.update(record, func(c *NestedCall) {
			c.ID, c.DurationMs = outcome.ToolCall.ID, &duration
			if outcome.IsError {
				c.Status = StatusError
				if callCtx.Err() != nil {
					c.Status = StatusCancelled
				}
				text := textOf(resultOf(outcome))
				if text == "" {
					text = fmt.Sprintf("Tool %q failed", tool.Name)
				}
				c.Error = truncateText(text, errorPreviewChars)
			} else {
				c.Status = StatusOK
			}
		})
		return toScriptValue(tool, outcome)
	}
}

func resultOf(outcome extension.AgentToolCallOutcome) agent.AgentToolResult {
	result, _ := outcome.Result.(agent.AgentToolResult)
	return result
}

// isNamespaceName reports whether query names the namespace: its name, its script identifier (`mcp__dev-radius` is
// `mcp__dev_radius`), or the part after its last `__` in either form (`dev-radius`, `dev_radius`).
//
// Ports packages/coding-agent/src/extensions/codemode/execute.ts (isNamespaceName).
func isNamespaceName(namespace, query string) bool {
	id, queryID := sandbox.ToCodemodeIdentifier(namespace), sandbox.ToCodemodeIdentifier(query)
	suffix := func(name string) (string, bool) {
		i := strings.LastIndex(name, "__")
		if i < 0 {
			return "", false
		}
		return name[i+2:], true
	}
	if namespace == query || id == queryID {
		return true
	}
	if tail, ok := suffix(namespace); ok && tail == query {
		return true
	}
	tail, ok := suffix(id)
	return ok && tail == queryID
}

// discoveryGlobals are `searchTools()`, `describeTool()`, and `describeNamespace()`: ranked search and lookup over the script's nested tools and their namespaces.
func discoveryGlobals(tools []extension.AgentTool, samples map[string]string, tc *extension.ToolContext) []sandbox.Tool {
	ranker := toolsearch.NewBm25Ranker()
	entry := func(name string) map[string]string {
		return map[string]string{"name": sandbox.ToCodemodeIdentifier(name), "description": samples[name]}
	}
	namespaceOf := func(name string) *extension.ToolNamespace {
		if tc == nil {
			return nil
		}
		for _, info := range tc.GetAllTools() {
			if info.Name == name {
				return info.Namespace
			}
		}
		return nil
	}
	return []sandbox.Tool{
		{Name: "searchTools", Spread: true, Execute: func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var args []json.RawMessage
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, err
			}
			var query string
			if len(args) == 0 || json.Unmarshal(args[0], &query) != nil || string(args[0]) == "null" {
				return nil, errors.New("searchTools() expects a query string")
			}
			limit, namespace := float64(toolsearch.DefaultLimit), ""
			if len(args) > 1 && string(args[1]) != "null" {
				var search map[string]json.RawMessage
				if json.Unmarshal(args[1], &search) == nil {
					if value, ok := search["limit"]; ok && string(value) != "null" {
						if json.Unmarshal(value, &limit) != nil || limit != math.Trunc(limit) || limit <= 0 {
							return nil, errors.New("searchTools() limit must be a positive integer")
						}
					}
					if value, ok := search["namespace"]; ok && string(value) != "null" {
						if json.Unmarshal(value, &namespace) != nil {
							return nil, errors.New("searchTools() namespace must be a string")
						}
					}
				}
			}
			var documents []toolsearch.Document
			for _, tool := range tools {
				toolNamespace := namespaceOf(tool.Name)
				if namespace != "" && (toolNamespace == nil || !isNamespaceName(toolNamespace.Name, namespace)) {
					continue
				}
				documents = append(documents, toolsearch.CreateDocument(infoOf(tool), toolNamespace))
			}
			entries := []map[string]string{}
			for _, match := range ranker.Rank(query, documents, toolsearch.LimitOf(limit)) {
				entries = append(entries, entry(match.Name))
			}
			return json.Marshal(entries)
		}},
		{Name: "describeTool", Spread: true, Execute: func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var args []json.RawMessage
			var name string
			if err := json.Unmarshal(raw, &args); err != nil || len(args) == 0 || json.Unmarshal(args[0], &name) != nil || string(args[0]) == "null" {
				return nil, errors.New("describeTool() expects a tool name")
			}
			index := slices.IndexFunc(tools, func(candidate extension.AgentTool) bool {
				return candidate.Name == name || sandbox.ToCodemodeIdentifier(candidate.Name) == name
			})
			if index < 0 {
				return nil, nil
			}
			return json.Marshal(samples[tools[index].Name])
		}},
		{Name: "describeNamespace", Spread: true, Execute: func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var args []json.RawMessage
			var name string
			if err := json.Unmarshal(raw, &args); err != nil || len(args) == 0 || json.Unmarshal(args[0], &name) != nil || string(args[0]) == "null" {
				return nil, errors.New("describeNamespace() expects a namespace name")
			}
			var namespace *extension.ToolNamespace
			names := []string{}
			for _, tool := range tools {
				toolNamespace := namespaceOf(tool.Name)
				if toolNamespace == nil || !isNamespaceName(toolNamespace.Name, name) {
					continue
				}
				if namespace == nil {
					namespace = toolNamespace
				}
				names = append(names, sandbox.ToCodemodeIdentifier(tool.Name))
			}
			if namespace == nil {
				return nil, nil
			}
			return json.Marshal(struct {
				Name         string   `json:"name"`
				Description  string   `json:"description,omitempty"`
				Instructions string   `json:"instructions,omitempty"`
				Tools        []string `json:"tools"`
			}{namespace.Name, namespace.Description, namespace.Instructions, names})
		}},
	}
}

func infoOf(tool extension.AgentTool) extension.ToolInfo {
	return extension.ToolInfo{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}
}
