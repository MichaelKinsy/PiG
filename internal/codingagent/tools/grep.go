package tools

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
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

// GrepOperations is upstream's GrepOperations: pluggable path checks. The search itself always runs the local ripgrep; to search a
// remote system, wrap the tool's Execute. Ports packages/coding-agent/src/core/tools/grep.ts.
type GrepOperations struct {
	// IsDirectory reports whether the path is a directory and fails when it does not exist.
	IsDirectory func(absolutePath string) (bool, error)
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

func (t *GrepTool) Name() string  { return "grep" }
func (t *GrepTool) Label() string { return "" }

func (t *GrepTool) Schema() ai.ToolSchema {
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:        "grep",
		Description: "Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to 100 matches or 50KB (whichever is hit first). Long lines are truncated to 500 chars.",
	}, `{"type":"object","required":["pattern"],"properties":{
		"pattern":{"type":"string","description":"Search pattern (regex or literal string)"},
		"path":{"type":"string","description":"Directory or file to search (default: current directory)"},
		"glob":{"type":"string","description":"Filter files by glob pattern, relative to path, e.g. '*.ts', 'src/**/*.ts' or '**/*.spec.ts'. A pattern without '/' matches file names at any depth"},
		"ignoreCase":{"type":"boolean","description":"Case-insensitive search (default: false)"},
		"literal":{"type":"boolean","description":"Treat pattern as literal string instead of regex (default: false)"},
		"context":{"type":"number","description":"Number of lines to show before and after each match (default: 0)"},
		"limit":{"type":"number","description":"Maximum number of matches to return (default: 100)"}
	}}`)
}

func (t *GrepTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

// grepDefaultLimit mirrors upstream DEFAULT_LIMIT.
const grepDefaultLimit = 100

// maxSafeInteger is Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

// grepEvent is one rg "match" or "context" event that carries a path and a line number.
type grepEvent struct {
	isMatch    bool
	filePath   string
	lineNumber int
	lineText   string
}

// grepLeadingDotSlash is the "./" prefix of a glob (after an optional "!") that rg never matches.
var grepLeadingDotSlash = lazyregexp.New(`^(!?)(?:\./)+`)

// Execute mirrors upstream grep.ts execute: resolve the path, stream `rg --json` events with no line-length cap (upstream reads with
// readline), take the context lines from rg's own `context` events, stop rg once the match limit is proved to have cut results off,
// surface rg's stderr on failure, then apply byte truncation and upstream's notices.
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

	// Clamp so the number never takes exponent notation, which rg rejects.
	contextValue := 0.0
	if p.Context != nil && *p.Context > 0 {
		contextValue = math.Min(math.Floor(*p.Context), maxSafeInteger)
	}
	effectiveLimit := float64(grepDefaultLimit)
	if p.Limit != nil {
		effectiveLimit = math.Floor(*p.Limit)
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
		// rg never matches a leading "./" ("./src/*.ts" finds nothing), so drop it; "!" negates.
		args = append(args, "--glob", grepLeadingDotSlash.ReplaceAllString(p.Glob, "$1"))
	}
	// rg emits the surrounding lines as `context` events, so no matched file is ever read here (#9276).
	if contextValue > 0 {
		args = append(args, "--context", jsNumber(contextValue))
	}
	// rg roots --glob at its own cwd, so a directory search runs inside searchPath (#11807). It searches "." because rg canonicalizes
	// its cwd: with an absolute path, a symlinked searchPath would no longer lie under the glob root.
	runDir, target := cwd, searchPath
	if isDirectory {
		runDir, target = searchPath, "."
	}
	args = append(args, "--", p.Pattern, target)

	f := grepFormatter{searchPath: searchPath, isDirectory: isDirectory}
	// No line limit: the match limit already capped rows.
	// Number.MAX_SAFE_INTEGER is observable in the truncation details, even though only bytes cap this tool.
	// The output is collected head-first: lines past the 50KB the result can show are counted, not built (#9276).
	joined := newHeadJoin(truncationLimits(DefaultMaxBytes, 1<<53-1))
	// The size of the full output, including lines past the byte cap that are counted but not stored.
	outputBytes, outputLineCount := 0, 0
	matchCount := 0
	matchLimitReached := false
	lastFile := ""
	// Set at the last allowed match. rg still sends that match's trailing context, kept up to trailingContextEnd, and the limit notice
	// waits for proof of one more match.
	limitHit := false
	trailingContextEnd := 0.0
	done := false
	pushLine := func(e grepEvent) {
		lastFile = e.filePath
		length := f.emit(e, joined)
		// outputBytes counts a newline after every line; the joined output has one fewer.
		outputBytes += length + 1
		outputLineCount++
	}
	handle := func(line string) (finish bool) {
		if done || jsstring.Trim(line) == "" {
			return false
		}
		e, ok := parseRipgrepEvent(line)
		if !ok {
			return false
		}
		if !limitHit {
			pushLine(e)
			if !e.isMatch {
				return false
			}
			matchCount++
			if float64(matchCount) >= effectiveLimit {
				limitHit = true
				trailingContextEnd = float64(e.lineNumber) + contextValue
			}
			return false
		}
		inWindow := e.filePath == lastFile && float64(e.lineNumber) <= trailingContextEnd
		// A match inside the last match's trailing context is shown as context.
		isMatch := e.isMatch
		if inWindow {
			e.isMatch = false
			pushLine(e)
		}
		// Only another match proves that the limit cut results off, whether or not it was shown as context.
		if isMatch {
			matchLimitReached = true
		}
		if matchLimitReached && !(inWindow && float64(e.lineNumber) < trailingContextEnd) {
			done = true
			return true
		}
		return false
	}

	run, err := runRipgrep(ctx, rgPath, runDir, args, handle)
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
	if matchCount == 0 {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "No matches found"}}}, nil
	}

	tr := joined.Result()
	tr.TotalLines = outputLineCount
	tr.TotalBytes = outputBytes - 1
	output := tr.Content
	var notices []string
	details := &GrepDetails{}
	if matchLimitReached {
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
	killedDueToLimit bool
	exitCode         int
	stderr           string
}

// runRipgrep streams rg's JSON lines to handle. Lines are read without a length cap and the pipe is always drained, so a huge match
// line can neither stall rg nor drop later events; when handle returns true rg is killed and the rest of its output discarded.
func runRipgrep(ctx context.Context, rgPath, dir string, args []string, handle func(line string) (finish bool)) (ripgrepRun, error) {
	//nolint:gosec // G204 (class b): program is rg resolved by PiG's tools manager; args are fixed flags, then -- and the pattern and search path from the grep tool call, so the model cannot reach an rg option.
	cmd := exec.CommandContext(ctx, rgPath, args...)
	// Upstream's spawn has no cwd option for a file search, so on Windows libuv finds rg from
	// PiG's working directory: the lookup precedes cmd.Dir.
	nodespawn.SetProgram(cmd)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ripgrepRun{}, err
	}
	if err := cmd.Start(); err != nil {
		return ripgrepRun{}, err
	}
	var run ripgrepRun
	reader := bufio.NewReader(stdout)
	for {
		line, readErr := reader.ReadString('\n')
		if !run.killedDueToLimit && handle(line) {
			run.killedDueToLimit = true
			_ = cmd.Process.Kill()
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
		return run, waitErr
	}
	return run, nil
}

// rgText is a ripgrep JSON text field: valid UTF-8 arrives as text, anything else base64-encoded as bytes.
type rgText struct {
	Text  *string `json:"text"`
	Bytes *string `json:"bytes"`
}

// decode is upstream decodeRgText: the text, or the decoded bytes (invalid UTF-8 becomes U+FFFD, as Buffer.toString does).
func (t *rgText) decode() string {
	if t == nil {
		return ""
	}
	if t.Text != nil {
		return *t.Text
	}
	if t.Bytes != nil {
		if data, err := base64.StdEncoding.DecodeString(*t.Bytes); err == nil {
			return decodeText(data)
		}
	}
	return ""
}

// parseRipgrepEvent reads a "match" or "context" event that carries a path and a line number; upstream skips every other line.
func parseRipgrepEvent(line string) (grepEvent, bool) {
	var event struct {
		Type string `json:"type"`
		Data struct {
			Path       *rgText  `json:"path"`
			LineNumber *float64 `json:"line_number"`
			Lines      *rgText  `json:"lines"`
		} `json:"data"`
	}
	// upstream: coding-agent/src/core/tools/grep.ts:JSON.parse
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return grepEvent{}, false
	}
	isMatch := event.Type == "match"
	if !isMatch && event.Type != "context" {
		return grepEvent{}, false
	}
	d := event.Data
	filePath := d.Path.decode()
	if filePath == "" || d.LineNumber == nil {
		return grepEvent{}, false
	}
	//portlint:allow numbers ripgrep reports a line number as a small positive integer, so the conversion cannot leave the int range
	return grepEvent{isMatch: isMatch, filePath: filePath, lineNumber: int(*d.LineNumber), lineText: d.Lines.decode()}, true
}

// grepFormatter formats the rows of a grep result (upstream formatPath and pushLine).
type grepFormatter struct {
	searchPath     string
	isDirectory    bool
	linesTruncated bool
}

func (f *grepFormatter) formatPath(filePath string) string {
	if f.isDirectory {
		abs := filePath
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(f.searchPath, abs)
		}
		if rel, err := filepath.Rel(f.searchPath, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(filePath)
}

func (f *grepFormatter) truncate(line string) string {
	truncated := TruncateLine(line, GrepMaxLineLength)
	text, wasTruncated := truncated.Text, truncated.WasTruncated
	if wasTruncated {
		f.linesTruncated = true
	}
	return text
}

// stripLineTerminator is lineText.replace(/\r?\n?$/, ""): only the line terminator goes, a lone CR inside a line is content.
func stripLineTerminator(lineText string) string {
	switch {
	case strings.HasSuffix(lineText, "\r\n"):
		return lineText[:len(lineText)-2]
	case strings.HasSuffix(lineText, "\n"), strings.HasSuffix(lineText, "\r"):
		return lineText[:len(lineText)-1]
	}
	return lineText
}

// emit adds the row of one event to out and returns its length in bytes. Once out holds every byte its result can show, a row is only
// measured: its length and whether its text was cut are what the result and the notice still read.
func (f *grepFormatter) emit(e grepEvent, out *headJoin) int {
	relativePath := f.formatPath(e.filePath)
	sanitized := stripLineTerminator(e.lineText)
	separator := "-"
	if e.isMatch {
		separator = ":"
	}
	if out.full() && !strings.Contains(sanitized, "\n") {
		length := len(relativePath) + 1 + decimalBytes(e.lineNumber) + 2 + f.truncatedBytes(sanitized)
		out.AddLength(length)
		return length
	}
	row := relativePath + separator + strconv.Itoa(e.lineNumber) + separator + " " + f.truncate(sanitized)
	out.Add(row)
	return len(row)
}

// truncatedBytes is len(f.truncate(line)), recording a cut line as f.truncate does.
func (f *grepFormatter) truncatedBytes(line string) int {
	n, wasTruncated := truncatedLineBytes(line, GrepMaxLineLength)
	if wasTruncated {
		f.linesTruncated = true
	}
	return n
}

// GrepMaxLineLength matches upstream truncate.ts GREP_MAX_LINE_LENGTH.
const GrepMaxLineLength = GrepMaxLineLengthUpstream
