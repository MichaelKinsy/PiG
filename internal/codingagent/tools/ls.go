package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

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
	// Operations delegates the filesystem reads; nil selects the local filesystem (upstream options.operations).
	Operations *LsOperations
}

// LsStat is the `{ isDirectory: () => boolean }` that LsOperations.stat resolves (ls.ts:34).
type LsStat interface {
	IsDirectory() bool
}

// fileInfoStat is the LsStat of a local file system entry.
type fileInfoStat struct{ fs.FileInfo }

func (info fileInfoStat) IsDirectory() bool { return info.IsDir() }

// LsOperations is upstream's LsOperations: pluggable directory reads, for example over SSH. Each callback completes before Execute continues.
// Ports packages/coding-agent/src/core/tools/ls.ts.
type LsOperations struct {
	// Exists reports whether the path exists.
	Exists func(absolutePath string) (bool, error)
	// Stat describes a file or directory and fails when it is not found.
	Stat func(absolutePath string) (LsStat, error)
	// Readdir lists the entry names of a directory.
	Readdir func(absolutePath string) ([]string, error)
}

func (t *LsTool) operations() LsOperations {
	if t.Operations != nil {
		return *t.Operations
	}
	return LsOperations{
		Exists: func(path string) (bool, error) { _, err := os.Stat(path); return err == nil, nil },
		Stat: func(path string) (LsStat, error) {
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			return fileInfoStat{info}, nil
		},
		Readdir: func(path string) ([]string, error) {
			dir, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			defer func() { _ = dir.Close() }()
			return dir.Readdirnames(-1)
		},
	}
}

func (t *LsTool) Name() string  { return "ls" }
func (t *LsTool) Label() string { return "" }

func (t *LsTool) Schema() ai.ToolSchema {
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:        "ls",
		Description: "List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to 500 entries or 50KB (whichever is hit first).",
	}, `{"type":"object","properties":{
		"path":{"type":"string","description":"Directory to list (default: current directory)"},
		"limit":{"type":"number","description":"Maximum number of entries to return (default: 500)"}
	}}`)
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
	cwd, err := toolCWD(ctx, t.CWD)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	dirPath, err := resolvePath(cwd, target)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	effectiveLimit := float64(lsDefaultLimit)
	if p.Limit != nil {
		effectiveLimit = *p.Limit
	}
	ops := t.operations()
	if exists, err := ops.Exists(dirPath); err != nil || !exists {
		return lsError("Path not found: " + dirPath), nil
	}
	info, err := ops.Stat(dirPath)
	if err != nil {
		return lsError("Path not found: " + dirPath), nil
	}
	if !info.IsDirectory() {
		return lsError("Not a directory: " + dirPath), nil
	}
	entries, err := ops.Readdir(dirPath)
	if err != nil {
		return lsError("Cannot read directory: " + NodeFSError(err, "scandir", dirPath)), nil
	}

	// Sort alphabetically, case-insensitive (upstream ls.ts:109 `a.toLowerCase().localeCompare(b.toLowerCase())`): localeCompare is ICU root
	// collation, and Array.prototype.sort is stable. A Collator keeps iterator state, so each call owns one: ls runs in parallel mode.
	collator := collate.New(language.Und)
	slices.SortStableFunc(entries, func(a, b string) int {
		return collator.CompareString(strings.ToLower(a), strings.ToLower(b))
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
		entryInfo, err := ops.Stat(filepath.Join(dirPath, entry))
		if err != nil {
			continue
		}
		if entryInfo.IsDirectory() {
			entry += "/"
		}
		results = append(results, entry)
	}
	if ctx.Err() != nil {
		return lsError("Operation aborted"), nil
	}
	if len(results) == 0 {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "(empty directory)"}}}, nil
	}

	// Byte truncation only; the entry count is already capped.
	// Number.MAX_SAFE_INTEGER is observable in the truncation details, even though only bytes cap this tool.
	tr := TruncateHead(strings.Join(results, "\n"), truncationLimits(DefaultMaxBytes, 1<<53-1))
	output := tr.Content
	details := &LsDetails{}
	var notices []string
	if entryLimitReached {
		notices = append(notices, fmt.Sprintf("%s entries limit reached. Use limit=%s for more", jsNumber(effectiveLimit), jsNumber(effectiveLimit*2)))
		details.EntryLimitReached = effectiveLimit
	}
	if tr.Truncated {
		notices = append(notices, FormatSize(DefaultMaxBytes)+" limit reached")
		trc := tr
		details.Truncation = &trc
	}
	result := agent.AgentToolResult{}
	if len(notices) > 0 {
		output += "\n\n[" + strings.Join(notices, ". ") + "]"
		result.Details = details
	}
	result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: output}}
	return result, nil
}

func lsError(message string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: message}}, Details: map[string]any{}, IsError: true, Thrown: true}
}
