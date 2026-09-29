package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// ─── Write Tool ───────────────────────────────────────────────────────────────

type writeParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteOperations delegates file creation. Each callback completes before the mutation queue advances.
// Ports packages/coding-agent/src/core/tools/write.ts.
type WriteOperations struct {
	WriteFile func(string, string) error
	Mkdir     func(string) error
}

// WriteTool creates or overwrites files.
type WriteTool struct {
	CWD        string
	Queue      *FileMutationQueue // serialises concurrent writes
	Operations *WriteOperations
}

func (t *WriteTool) operations() WriteOperations {
	if t.Operations != nil {
		return *t.Operations
	}
	return WriteOperations{WriteFile: func(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }, Mkdir: func(path string) error { return os.MkdirAll(path, 0o755) }}
}

func (t *WriteTool) Name() string  { return "write" }
func (t *WriteTool) Label() string { return "" }

func (t *WriteTool) Schema() ai.ToolSchema {
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:                "write",
		Description:         "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		ConstrainedSampling: strictToolSampling(),
		PromptGuidelines: []string{
			"Use write only for new files or complete rewrites.",
		},
	}, `{"type":"object","required":["path","content"],"properties":{
		"path":{"type":"string","description":"Path to the file to write (relative or absolute)"},
		"content":{"type":"string","description":"Content to write to the file"}
	}}`)
}

// ExecutionMode is parallel: upstream's write definition sets no
// executionMode, and the file mutation queue serialises writes to one file.
func (t *WriteTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

// ReserveMutationOrder implements agent.QueueOrderable: the parallel
// dispatcher calls this synchronously, in tool-call order, before spawning
// this call's goroutine, so this write's place in the shared file mutation
// queue is fixed before goroutine scheduling can reorder it. Returns
// ok=false whenever Execute would not reach the queue either (invalid
// params), so no reservation is ever left un-awaited.
func (t *WriteTool) ReserveMutationOrder(rawParams json.RawMessage) (*agent.MutationTicket, bool) {
	if t.Queue == nil {
		return nil, false
	}
	var p writeParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return nil, false
	}
	path, err := resolvePath(t.CWD, p.Path)
	if err != nil {
		return nil, false
	}
	ticket, err := t.Queue.Reserve(path)
	if err != nil {
		return nil, false
	}
	return &agent.MutationTicket{Wait: ticket.Wait, Release: ticket.Release}, true
}

// Execute mirrors upstream write.ts execute: inside the file mutation queue
// create the parent directories and write the file, checking for an abort
// after each step.
func (t *WriteTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p writeParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("write: invalid params: %w", err)
	}
	cwd, err := toolCWD(ctx, t.CWD)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	path, err := resolvePath(cwd, p.Path)
	if err != nil {
		return agent.AgentToolResult{}, err
	}

	ops := t.operations()
	var failure string
	if err := runQueued(ctx, t.Queue, path, func() error {
		if ctx.Err() != nil {
			failure = "Operation aborted"
			return nil
		}
		if err := ops.Mkdir(filepath.Dir(path)); err != nil {
			failure = NodeFSError(err, "mkdir", filepath.Dir(path))
			return nil
		}
		if ctx.Err() != nil {
			failure = "Operation aborted"
			return nil
		}
		if err := ops.WriteFile(path, p.Content); err != nil {
			failure = NodeFSError(err, "open", path)
			return nil
		}
		if ctx.Err() != nil {
			failure = "Operation aborted"
		}
		return nil
	}); err != nil {
		// runQueued/Queue.With failed before the callback ever ran (for
		// example canonicalKey rejecting a symlink cycle): nothing was
		// written, so this must not be reported as a success. Mirrors
		// upstream write.ts awaiting withFileMutationQueue and propagating
		// its rejection instead of assuming the write happened.
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: err.Error()}}, Details: map[string]any{}, IsError: true}, nil
	}
	if failure != "" {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: failure}}, Details: map[string]any{}, IsError: true}, nil
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Successfully wrote to " + p.Path}}}, nil
}
