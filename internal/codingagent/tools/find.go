package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// ─── Find Tool ────────────────────────────────────────────────────────────────

type findParams struct {
	Path    string   `json:"path,omitempty"`
	Pattern string   `json:"pattern"`
	Limit   *float64 `json:"limit,omitempty"`
}

// findDefaultLimit mirrors upstream find.ts DEFAULT_LIMIT.
const findDefaultLimit = 1000

// FindGlobOptions is the glob operation's ignore list and requested result limit.
type FindGlobOptions struct {
	Ignore []string
	Limit  float64
}

// FindOperations delegates existence and glob lookup. Execute waits for each callback and discards its result after cancellation.
// Ports packages/coding-agent/src/core/tools/find.ts.
type FindOperations struct {
	Exists func(string) (bool, error)
	Glob   func(string, string, FindGlobOptions) ([]string, error)
}

// FindTool searches for files with fd, mirroring upstream find.ts.
//
// FdPath, when non-empty, pins the fd binary. Otherwise fd is resolved on
// every call through Tools (upstream ensureTool: <agentDir>/bin, PATH, then
// a download); with no Tools, PATH only.
type FindTool struct {
	CWD        string
	FdPath     string
	Tools      *ToolsManager
	Operations *FindOperations
}

func (t *FindTool) Name() string  { return "find" }
func (t *FindTool) Label() string { return "" }

func (t *FindTool) Schema() ai.ToolSchema {
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:        "find",
		Description: "Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to 1000 results or 50KB (whichever is hit first).",
	}, `{"type":"object","required":["pattern"],"properties":{
		"pattern":{"type":"string","description":"Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'"},
		"path":{"type":"string","description":"Directory to search in (default: current directory)"},
		"limit":{"type":"number","description":"Maximum number of results (default: 1000)"}
	}}`)
}

func (t *FindTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

// Execute mirrors upstream find.ts execute over fd.
func (t *FindTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p findParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("find: invalid params: %w", err)
	}
	if ctx.Err() != nil {
		return findError("Operation aborted"), nil
	}
	searchDir := p.Path
	if searchDir == "" {
		searchDir = "."
	}
	cwd, err := toolCWD(ctx, t.CWD)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	searchPath, err := resolvePath(cwd, searchDir)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	effectiveLimit := float64(findDefaultLimit)
	if p.Limit != nil {
		effectiveLimit = *p.Limit
	}

	if t.Operations != nil && t.Operations.Glob != nil {
		return t.findWithOperations(ctx, p.Pattern, searchPath, effectiveLimit), nil
	}

	fdPath := ensureSearchTool(ctx, t.FdPath, t.Tools, "fd")
	if ctx.Err() != nil {
		return findError("Operation aborted"), nil
	}
	if fdPath == "" {
		return findError("fd is not available and could not be downloaded"), nil
	}

	args := []string{"--glob", "--color=never", "--hidden"}
	// fd ignores .gitignore outside git repos unless --no-require-git; inside
	// a repo its git-aware default stops parent rules at nested repos
	// (upstream #5960).
	if !insideGitRepo(searchPath) {
		args = append(args, "--no-require-git")
	}
	args = append(args, "--max-results", jsNumber(effectiveLimit))
	// fd --glob matches the basename unless --full-path is set; in
	// --full-path mode it matches the absolute candidate path, so a
	// path-containing pattern needs a leading "**/".
	effectivePattern := p.Pattern
	if strings.Contains(p.Pattern, "/") {
		args = append(args, "--full-path")
		if !strings.HasPrefix(p.Pattern, "/") && !strings.HasPrefix(p.Pattern, "**/") && p.Pattern != "**" {
			effectivePattern = "**/" + p.Pattern
		}
		if runtime.GOOS == "windows" {
			effectivePattern = strings.ReplaceAll(effectivePattern, "/", `[/\\]`)
		}
	}
	args = append(args, "--", effectivePattern, searchPath)

	cmd := exec.CommandContext(ctx, fdPath, args...)
	// Upstream's spawn finds fd with libuv's search on Windows.
	nodespawn.SetProgram(cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		// Upstream's catch rejects with an error spawn throws unchanged; only
		// the child's error event adds the prefix.
		if spawnErr, ok := errors.AsType[*nodespawn.Error](err); ok && spawnErr.Thrown {
			return findError(err.Error()), nil
		}
		return findError("Failed to run fd: " + err.Error()), nil
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return findError("Operation aborted"), nil
	}
	lines := readlineLines(stdout.String())
	output := strings.Join(lines, "\n")
	if waitErr != nil && output == "" {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			code := -1
			if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
				code = exitErr.ExitCode()
			}
			msg = fmt.Sprintf("fd exited with code %d", code)
		}
		return findError(msg), nil
	}
	if output == "" {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "No files found matching pattern"}}}, nil
	}

	var relativized []string
	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" {
			continue
		}
		relativized = append(relativized, relativizeFindResultPath(line, searchPath))
	}
	return formatFindResults(relativized, effectiveLimit, false), nil
}

func (t *FindTool) findWithOperations(ctx context.Context, pattern, searchPath string, limit float64) agent.AgentToolResult {
	ops := t.Operations
	if ops.Exists == nil {
		return findError("ops.exists is not a function")
	}
	exists, err := ops.Exists(searchPath)
	if ctx.Err() != nil {
		return findError("Operation aborted")
	}
	if err != nil {
		return findError(err.Error())
	}
	if !exists {
		return findError("Path not found: " + searchPath)
	}
	results, err := ops.Glob(pattern, searchPath, FindGlobOptions{Ignore: []string{"**/node_modules/**", "**/.git/**"}, Limit: limit})
	if ctx.Err() != nil {
		return findError("Operation aborted")
	}
	if err != nil {
		return findError(err.Error())
	}
	if len(results) == 0 {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "No files found matching pattern"}}}
	}
	relativized := make([]string, len(results))
	for i, result := range results {
		relativized[i] = relativizeFindResultPath(result, searchPath)
	}
	return formatFindResults(relativized, limit, true)
}

func formatFindResults(relativized []string, effectiveLimit float64, custom bool) agent.AgentToolResult {
	resultLimitReached := float64(len(relativized)) >= effectiveLimit
	// Number.MAX_SAFE_INTEGER is observable in the truncation details, even though only bytes cap this tool.
	tr := TruncateHead(strings.Join(relativized, "\n"), DefaultMaxBytes, 1<<53-1)
	resultOutput := tr.Content
	details := &FindDetails{}
	var notices []string
	if resultLimitReached {
		notice := jsNumber(effectiveLimit) + " results limit reached"
		if !custom {
			notice += ". Use limit=" + jsNumber(effectiveLimit*2) + " for more, or refine pattern"
		}
		notices = append(notices, notice)
		details.ResultLimitReached = new(effectiveLimit)
	}
	if tr.Truncated {
		notices = append(notices, FormatSize(DefaultMaxBytes)+" limit reached")
		trc := tr
		details.Truncation = &trc
	}
	result := agent.AgentToolResult{}
	if len(notices) > 0 {
		resultOutput += "\n\n[" + strings.Join(notices, ". ") + "]"
		result.Details = details
	}
	result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: resultOutput}}
	return result
}

func findError(message string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: message}}, Details: map[string]any{}, IsError: true, Thrown: true}
}

// insideGitRepo reports whether searchPath or an ancestor has a .git entry.
func insideGitRepo(searchPath string) bool {
	for current := searchPath; ; {
		if fileExists(filepath.Join(current, ".git")) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

// readlineLines splits output the way Node readline emits "line" events:
// on "\n" (and "\r\n"), with no event for the empty text after a final
// newline.
func readlineLines(output string) []string {
	if output == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// relativizeFindResultPath mirrors upstream relativizeFindResultPath: an
// absolute result becomes relative to the search root, separators become
// "/", and a trailing separator (a directory) is kept.
func relativizeFindResultPath(resultPath, searchPath string) string {
	sep := string(filepath.Separator)
	hadTrailingSeparator := strings.HasSuffix(resultPath, sep) || (sep == `\` && strings.HasSuffix(resultPath, "/"))
	relativePath := resultPath
	if isNodeAbsolute(resultPath) {
		from, fromErr := nodepath.Resolve(searchPath)
		to, toErr := nodepath.Resolve(resultPath)
		if fromErr == nil && toErr == nil {
			relativePath = to
			if rel, err := filepath.Rel(from, to); err == nil {
				relativePath = rel
				if rel == "." {
					relativePath = ""
				}
			}
		}
	}
	posixPath := strings.Join(strings.Split(relativePath, sep), "/")
	if hadTrailingSeparator && !strings.HasSuffix(posixPath, "/") {
		return posixPath + "/"
	}
	return posixPath
}
