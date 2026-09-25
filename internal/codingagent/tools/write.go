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

// WriteTool creates or overwrites files.
type WriteTool struct {
	CWD   string
	Queue *FileMutationQueue // serialises concurrent writes
}

func (t *WriteTool) Name() string  { return "write" }
func (t *WriteTool) Label() string { return "" }

func (t *WriteTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{
		Name:        "write",
		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path to the file to write (relative or absolute)"},
				"content": map[string]any{"type": "string", "description": "Content to write to the file"},
			},
			"required": []string{"path", "content"},
		},
		ConstrainedSampling: strictToolSampling(),
		PromptGuidelines: []string{
			"Use write only for new files or complete rewrites.",
		},
	}
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
	ticket, err := t.Queue.Reserve(resolvePath(t.CWD, p.Path))
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
	path := resolvePath(t.CWD, p.Path)
	// Overwrite-vs-create is only for the TUI renderer; it is not SDK detail.
	info, statErr := os.Stat(path)
	overwrote := statErr == nil && info.Mode().IsRegular()

	var failure string
	if err := runQueued(ctx, t.Queue, path, func() error {
		if ctx.Err() != nil {
			failure = "Operation aborted"
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			failure = nodeFSError(err, "mkdir", filepath.Dir(path))
			return nil
		}
		if ctx.Err() != nil {
			failure = "Operation aborted"
			return nil
		}
		if err := os.WriteFile(path, []byte(p.Content), 0o644); err != nil {
			failure = nodeFSError(err, "open", path)
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
		return agent.AgentToolResult{Content: err.Error(), IsError: true}, nil
	}
	if failure != "" {
		return agent.AgentToolResult{Content: failure, IsError: true}, nil
	}
	return agent.AgentToolResult{
		Content: "Successfully wrote to " + p.Path,
		Details: &WriteDetails{Path: p.Path, Content: p.Content, Overwrote: overwrote},
	}, nil
}
