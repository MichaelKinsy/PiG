package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// ─── Grep Tool ────────────────────────────────────────────────────────────────

type grepParams struct {
	Pattern    string   `json:"pattern"`
	Path       string   `json:"path,omitempty"`
	Glob       string   `json:"glob,omitempty"`       // file pattern filter (e.g. '*.ts')
	IgnoreCase bool     `json:"ignoreCase,omitempty"` // case-insensitive search
	Literal    bool     `json:"literal,omitempty"`    // treat pattern as literal string (--fixed-strings)
	Context    *float64 `json:"context,omitempty"`    // lines of context around each match
	Limit      *float64 `json:"limit,omitempty"`      // max matches (default: 100)
}

// GrepTool runs ripgrep to search files, mirroring upstream grep.ts.
//
// RgPath, when non-empty, pins the ripgrep binary. Otherwise rg is resolved
// on every call through Tools (upstream ensureTool: <agentDir>/bin, PATH,
// then a download); with no Tools, PATH only.
type GrepTool struct {
	CWD    string
	RgPath string
	Tools  *ToolsManager
	// Operations delegates the filesystem reads; nil selects the local filesystem (upstream options.operations).
	Operations *GrepOperations
}

// GrepOperations is upstream's GrepOperations: pluggable path and file reads for the search path and the context lines.
// Ports packages/coding-agent/src/core/tools/grep.ts.
type GrepOperations struct {
	// IsDirectory reports whether the path is a directory and fails when it does not exist.
	IsDirectory func(absolutePath string) (bool, error)
	// ReadFile reads a file for its context lines; nil streams the local file.
	ReadFile func(absolutePath string) (string, error)
}

func (t *GrepTool) operations() GrepOperations {
	if t.Operations != nil {
		return *t.Operations
	}
	return GrepOperations{
		IsDirectory: func(path string) (bool, error) {
			info, err := os.Stat(path)
			if err != nil {
				return false, err
			}
			return info.IsDir(), nil
		},
	}
}

// openFile opens a matched file for its context lines. The local filesystem
// streams the file; a custom ReadFile returns the whole text, as upstream's
// readFile does.
func (t *GrepTool) openFile(path string) (io.ReadCloser, error) {
	if t.Operations == nil || t.Operations.ReadFile == nil {
		return os.Open(path)
	}
	text, err := t.Operations.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(text)), nil
}

func (t *GrepTool) Name() string  { return "grep" }
func (t *GrepTool) Label() string { return "" }

func (t *GrepTool) Schema() ai.ToolSchema {
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:        "grep",
		Description: "Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to 100 matches or 50KB (whichever is hit first). Long lines are truncated to 500 chars.",
	}, `{"type":"object","required":["pattern"],"properties":{
		"pattern":{"type":"string","description":"Search pattern (regex or literal string)"},
		"path":{"type":"string","description":"Directory or file to search (default: current directory)"},
		"glob":{"type":"string","description":"Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'"},
		"ignoreCase":{"type":"boolean","description":"Case-insensitive search (default: false)"},
		"literal":{"type":"boolean","description":"Treat pattern as literal string instead of regex (default: false)"},
		"context":{"type":"number","description":"Number of lines to show before and after each match (default: 0)"},
		"limit":{"type":"number","description":"Maximum number of matches to return (default: 100)"}
	}}`)
}

func (t *GrepTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

// grepDefaultLimit mirrors upstream DEFAULT_LIMIT.
const grepDefaultLimit = 100

// grepMatch is one rg "match" event.
type grepMatch struct {
	filePath   string
	lineNumber int
	lineText   *string
}

// Execute mirrors upstream grep.ts execute: resolve the path, stream
// `rg --json` events with no line-length cap (upstream reads with
// readline), stop rg at the match limit, surface rg's stderr on failure,
// then format matches (with context lines read from the files) and apply
// byte truncation and upstream's notices.
func (t *GrepTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p grepParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("grep: invalid params: %w", err)
	}
	if ctx.Err() != nil {
		return grepError("Operation aborted"), nil
	}
	rgPath := ensureSearchTool(ctx, t.RgPath, t.Tools, "rg")
	if rgPath == "" {
		return grepError("ripgrep (rg) is not available and could not be downloaded"), nil
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
	ops := t.operations()
	isDirectory, err := ops.IsDirectory(searchPath)
	if err != nil {
		return grepError("Path not found: " + searchPath), nil
	}

	contextValue := 0.0
	if p.Context != nil && *p.Context > 0 {
		contextValue = *p.Context
	}
	effectiveLimit := float64(grepDefaultLimit)
	if p.Limit != nil {
		effectiveLimit = *p.Limit
	}
	effectiveLimit = math.Max(1, effectiveLimit)

	// --no-config: the tool contract (literal, glob, limit, ...) must not depend
	// on ambient user config like RIPGREP_CONFIG_PATH.
	args := []string{"--no-config", "--json", "--line-number", "--color=never", "--hidden"}
	if p.IgnoreCase {
		args = append(args, "--ignore-case")
	}
	if p.Literal {
		args = append(args, "--fixed-strings")
	}
	if p.Glob != "" {
		args = append(args, "--glob", p.Glob)
	}
	args = append(args, "--", p.Pattern, searchPath)

	matches, run, err := runRipgrep(ctx, rgPath, cwd, args, effectiveLimit)
	if err != nil {
		// Upstream's catch rejects with an error spawn throws unchanged; only
		// the child's error event adds the prefix.
		if spawnErr, ok := errors.AsType[*nodespawn.Error](err); ok && spawnErr.Thrown {
			return grepError(err.Error()), nil
		}
		return grepError("Failed to run ripgrep: " + err.Error()), nil
	}
	if ctx.Err() != nil {
		return grepError("Operation aborted"), nil
	}
	if !run.killedDueToLimit && run.exitCode != 0 && run.exitCode != 1 {
		msg := jsstring.Trim(run.stderr)
		if msg == "" {
			msg = fmt.Sprintf("ripgrep exited with code %d", run.exitCode)
		}
		return grepError(msg), nil
	}
	if run.matchCount == 0 {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "No matches found"}}}, nil
	}

	f := grepFormatter{searchPath: searchPath, isDirectory: isDirectory, contextValue: contextValue, openFile: t.openFile}
	defer f.close()

	// No line limit: the match limit already capped rows.
	// Number.MAX_SAFE_INTEGER is observable in the truncation details, even though only bytes cap this tool.
	// The output is collected head-first: lines past the 50KB the result can show are counted, not built (#9276).
	joined := newHeadJoin(truncationLimits(DefaultMaxBytes, 1<<53-1))
	for _, m := range matches {
		f.emitMatch(m, joined)
	}
	tr := joined.Result()
	output := tr.Content
	var notices []string
	details := &GrepDetails{}
	if run.matchLimitReached {
		notices = append(notices, fmt.Sprintf("%s matches limit reached. Use limit=%s for more, or refine pattern",
			jsNumber(effectiveLimit), jsNumber(effectiveLimit*2)))
		details.MatchLimitReached = effectiveLimit
	}
	if tr.Truncated {
		notices = append(notices, FormatSize(DefaultMaxBytes)+" limit reached")
		trc := tr
		details.Truncation = &trc
	}
	if f.linesTruncated {
		notices = append(notices, fmt.Sprintf("Some lines truncated to %d chars. Use read tool to see full lines", GrepMaxLineLength))
		details.LinesTruncated = true
	}
	if len(notices) > 0 {
		output += "\n\n[" + strings.Join(notices, ". ") + "]"
		shareTruncatedContent(details.Truncation, output)
	}
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: output}}}
	if len(notices) > 0 {
		result.Details = details
	}
	return result, nil
}

func grepError(message string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: message}}, Details: map[string]any{}, IsError: true, Thrown: true}
}

// ripgrepRun is the outcome of one rg process.
type ripgrepRun struct {
	matchCount        int
	matchLimitReached bool
	killedDueToLimit  bool
	exitCode          int
	stderr            string
}

// runRipgrep streams rg's JSON events. Lines are read without a length cap
// and the pipe is always drained, so a huge match line can neither stall rg
// nor drop later matches; at the match limit rg is killed and the rest of
// its output discarded.
func runRipgrep(ctx context.Context, rgPath, dir string, args []string, limit float64) ([]grepMatch, ripgrepRun, error) {
	cmd := exec.CommandContext(ctx, rgPath, args...)
	// Upstream's spawn has no cwd option, so on Windows libuv finds rg from
	// PiG's working directory: the lookup precedes cmd.Dir.
	nodespawn.SetProgram(cmd)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, ripgrepRun{}, err
	}
	if err := cmd.Start(); err != nil {
		return nil, ripgrepRun{}, err
	}
	var run ripgrepRun
	var matches []grepMatch
	reader := bufio.NewReader(stdout)
	for {
		line, readErr := reader.ReadString('\n')
		if jsstring.Trim(line) != "" && float64(run.matchCount) < limit {
			if m, ok := parseRipgrepMatch(line); ok {
				run.matchCount++
				if m != nil {
					matches = append(matches, *m)
				}
				if float64(run.matchCount) >= limit {
					run.matchLimitReached = true
					run.killedDueToLimit = true
					_ = cmd.Process.Kill()
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	run.stderr = stderr.String()
	if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
		run.exitCode = exitErr.ExitCode()
	} else if waitErr != nil && !run.killedDueToLimit && ctx.Err() == nil {
		return nil, run, waitErr
	}
	return matches, run, nil
}

// parseRipgrepMatch reports whether line is a "match" event, and returns the
// match when it carries a path and line number (upstream skips others but
// still counts them).
func parseRipgrepMatch(line string) (*grepMatch, bool) {
	var event struct {
		Type string `json:"type"`
		Data struct {
			Path *struct {
				Text *string `json:"text"`
			} `json:"path"`
			LineNumber *float64 `json:"line_number"`
			Lines      *struct {
				Text *string `json:"text"`
			} `json:"lines"`
		} `json:"data"`
	}
	// upstream: coding-agent/src/core/tools/grep.ts:JSON.parse
	if err := json.Unmarshal([]byte(line), &event); err != nil || event.Type != "match" {
		return nil, false
	}
	d := event.Data
	if d.Path == nil || d.Path.Text == nil || *d.Path.Text == "" || d.LineNumber == nil {
		return nil, true
	}
	//portlint:allow numbers ripgrep reports a line number as a small positive integer, so the conversion cannot leave the int range
	m := &grepMatch{filePath: *d.Path.Text, lineNumber: int(*d.LineNumber)}
	if d.Lines != nil {
		m.lineText = d.Lines.Text
	}
	return m, true
}

// grepFormatter mirrors upstream's formatPath, getFileLines and formatBlock.
// pig divergence (D151): context lines come from a window over the
// matched file, not a whole-file array, so memory follows the context size and
// not the file size.
type grepFormatter struct {
	searchPath     string
	isDirectory    bool
	contextValue   float64
	openFile       func(absolutePath string) (io.ReadCloser, error)
	window         *grepLineWindow
	windowPath     string
	linesTruncated bool
}

func (f *grepFormatter) formatPath(filePath string) string {
	if f.isDirectory {
		if rel, err := filepath.Rel(f.searchPath, filePath); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(filePath)
}

// fileWindow returns the window for filePath. Matches of one file are
// consecutive, so only the current file's window is kept; a match above the
// window's first line reopens the file.
func (f *grepFormatter) fileWindow(filePath string, firstLine float64) *grepLineWindow {
	if f.window != nil && f.windowPath == filePath && firstLine >= float64(f.window.keepFrom) {
		return f.window
	}
	f.close()
	f.window = &grepLineWindow{open: func() (io.ReadCloser, error) { return f.openFile(filePath) }, keepFrom: 1}
	f.windowPath = filePath
	return f.window
}

func (f *grepFormatter) close() {
	if f.window != nil {
		f.window.close()
		f.window = nil
	}
}

func (f *grepFormatter) truncate(line string) string {
	truncated := TruncateLine(line, GrepMaxLineLength)
	text, wasTruncated := truncated.Text, truncated.WasTruncated
	if wasTruncated {
		f.linesTruncated = true
	}
	return text
}

// emitMatch adds the output lines of one match to out (grep.ts:198-277). Once out holds every byte its result can show, a line is only measured: its length and whether its text was cut are what the result and the notice still read.
func (f *grepFormatter) emitMatch(m grepMatch, out *headJoin) {
	relativePath := f.formatPath(m.filePath)
	if f.contextValue == 0 && m.lineText != nil {
		sanitized := strings.ReplaceAll(*m.lineText, "\r\n", "\n")
		sanitized = strings.TrimSuffix(strings.ReplaceAll(sanitized, "\r", ""), "\n")
		if out.full() && !strings.Contains(sanitized, "\n") {
			out.AddLength(len(relativePath) + 1 + decimalBytes(m.lineNumber) + 2 + f.truncatedBytes(sanitized))
			return
		}
		out.Add(fmt.Sprintf("%s:%d: %s", relativePath, m.lineNumber, f.truncate(sanitized)))
		return
	}
	lineNumber := float64(m.lineNumber)
	start, end := lineNumber, lineNumber
	if f.contextValue > 0 {
		start = math.Max(1, lineNumber-f.contextValue)
		end = lineNumber + f.contextValue
	}
	w := f.fileWindow(m.filePath, start)
	w.discardBefore(start)
	total := w.countAtLeast(end)
	if total == 0 {
		out.Add(fmt.Sprintf("%s:%d: (unable to read file)", relativePath, m.lineNumber))
		return
	}
	if f.contextValue > 0 {
		end = math.Min(float64(total), end)
	}
	for current := start; current <= end; current++ {
		lineText := ""
		//portlint:allow numbers current is at most the end bound, which is at most the line count, so the conversion stays in range
		if current == math.Trunc(current) && current >= 1 && int(current) <= total {
			//portlint:allow numbers the guard above bounds current by the line count
			lineText = w.line(int(current))
		}
		lineText = strings.ReplaceAll(lineText, "\r", "")
		if out.full() && !strings.Contains(lineText, "\n") {
			out.AddLength(len(relativePath) + 1 + numberBytes(current) + 2 + f.truncatedBytes(lineText))
			continue
		}
		truncated := f.truncate(lineText)
		if current == lineNumber {
			out.Add(fmt.Sprintf("%s:%s: %s", relativePath, jsNumber(current), truncated))
		} else {
			out.Add(fmt.Sprintf("%s-%s- %s", relativePath, jsNumber(current), truncated))
		}
	}
}

// truncatedBytes is len(f.truncate(line)), recording a cut line as f.truncate does.
func (f *grepFormatter) truncatedBytes(line string) int {
	n, wasTruncated := truncatedLineBytes(line, GrepMaxLineLength)
	if wasTruncated {
		f.linesTruncated = true
	}
	return n
}

// numberBytes is len(jsNumber(v)) without the string for the whole line numbers grep writes.
func numberBytes(v float64) int {
	if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
		//portlint:allow numbers the guard admits only integral values below 2^53, which int holds exactly and jsNumber writes as plain decimals
		return decimalBytes(int(v))
	}
	return len(jsNumber(v))
}

// GrepMaxLineLength matches upstream truncate.ts GREP_MAX_LINE_LENGTH.
const GrepMaxLineLength = GrepMaxLineLengthUpstream
