package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// ─── Ls Tool ─────────────────────────────────────────────────────────────────

type lsParams struct {
	Path  string   `json:"path,omitempty"`
	Limit *float64 `json:"limit,omitempty"` // upstream: limit param (default: 500)
}

// lsDefaultLimit mirrors upstream ls.ts DEFAULT_LIMIT.
const lsDefaultLimit = 500

// LsTool lists directory contents.
type LsTool struct {
	CWD string
}

func (t *LsTool) Name() string  { return "ls" }
func (t *LsTool) Label() string { return "" }

func (t *LsTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{
		Name:        "ls",
		Description: "List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to 500 entries or 50KB (whichever is hit first).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Directory to list (default: current directory)"},
				"limit": map[string]any{"type": "number", "description": "Maximum number of entries to return (default: 500)"},
			},
		},
	}
}

func (t *LsTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

// Execute mirrors upstream ls.ts execute.
func (t *LsTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p lsParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("ls: invalid params: %w", err)
	}
	if ctx.Err() != nil {
		return lsError("Operation aborted"), nil
	}
	target := p.Path
	if target == "" {
		target = "."
	}
	dirPath := resolvePath(t.CWD, target)
	effectiveLimit := float64(lsDefaultLimit)
	if p.Limit != nil {
		effectiveLimit = *p.Limit
	}
	info, err := os.Stat(dirPath)
	if err != nil {
		return lsError("Path not found: " + dirPath), nil
	}
	if !info.IsDir() {
		return lsError("Not a directory: " + dirPath), nil
	}
	dir, err := os.Open(dirPath)
	var entries []string
	if err == nil {
		entries, err = dir.Readdirnames(-1)
		_ = dir.Close()
	}
	if err != nil {
		return lsError("Cannot read directory: " + nodeFSError(err, "scandir", dirPath)), nil
	}

	// Sort alphabetically, case-insensitive (upstream ls.ts).
	slices.SortFunc(entries, func(a, b string) int {
		return cmp.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	var results []string
	entryLimitReached := false
	for _, entry := range entries {
		if float64(len(results)) >= effectiveLimit {
			entryLimitReached = true
			break
		}
		// stat follows symlinks; entries that cannot be stat'ed (broken
		// links) are skipped, as upstream does.
		entryInfo, err := os.Stat(filepath.Join(dirPath, entry))
		if err != nil {
			continue
		}
		if entryInfo.IsDir() {
			entry += "/"
		}
		results = append(results, entry)
	}
	if ctx.Err() != nil {
		return lsError("Operation aborted"), nil
	}
	if len(results) == 0 {
		return agent.AgentToolResult{Content: "(empty directory)"}, nil
	}

	// Byte truncation only; the entry count is already capped.
	tr := TruncateHead(strings.Join(results, "\n"), DefaultMaxBytes, math.MaxInt)
	output := tr.Content
	details := &LsDetails{}
	var notices []string
	if entryLimitReached {
		notices = append(notices, fmt.Sprintf("%s entries limit reached. Use limit=%s for more", jsNumber(effectiveLimit), jsNumber(effectiveLimit*2)))
		details.EntryLimitReached = int(effectiveLimit)
	}
	if tr.Truncated {
		notices = append(notices, FormatSize(DefaultMaxBytes)+" limit reached")
		trc := tr
		details.Truncation = &trc
	}
	result := agent.AgentToolResult{Content: output}
	if len(notices) > 0 {
		result.Content += "\n\n[" + strings.Join(notices, ". ") + "]"
		result.Details = details
	}
	return result, nil
}

func lsError(message string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: message, IsError: true}
}
