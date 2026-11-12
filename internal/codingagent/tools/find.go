package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/minimatch"
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

	// gitRepoLookup replaces the .git lookup above the search path in tests.
	gitRepoLookup func(searchPath string) bool
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
	// Pi main 676af1d77 (#11747) reads a limit below 1 as 1 (fd treats --max-results 0 as unlimited)
	// and floors a fractional one.
	effectiveLimit = math.Max(1, math.Floor(effectiveLimit))

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

	// Pi 1.5.1 (#9262, #11972): see newFindFdInvocation for the arguments and the Windows path matcher.
	invocation, err := newFindFdInvocation(p.Pattern, searchPath, effectiveLimit, runtime.GOOS == "windows", t.insideGitRepo(searchPath))
	if err != nil {
		return findError(err.Error()), nil
	}
	// An abort during the .git lookup above already returned the aborted result, but there was no child to stop yet. Do not start fd (Pi 1.5.1 #11974).
	if ctx.Err() != nil {
		return findError("Operation aborted"), nil
	}

	// A Windows path matcher lists every entry, so fd stops once one match past the limit arrived (--max-results would count non-matching entries).
	fdCtx, stopFd := context.WithCancel(ctx)
	defer stopFd()
	//nolint:gosec // G204 (class b): program is fd resolved by PiG's tools manager; args are fixed flags, then -- and the pattern and search path from the find tool call, so the model cannot reach an fd option.
	cmd := exec.CommandContext(fdCtx, fdPath, invocation.args...)
	// Upstream's spawn finds fd with libuv's search on Windows.
	nodespawn.SetProgram(cmd)
	var stdout, stderr strings.Builder
	var filter *findPathFilter
	if invocation.pathMatcher != nil {
		filter = &findPathFilter{matcher: invocation.pathMatcher, limit: effectiveLimit, stop: stopFd}
		cmd.Stdout = filter
	} else {
		cmd.Stdout = &stdout
	}
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
	if filter != nil {
		filter.finish()
		stdout.WriteString(filter.kept.String())
	}
	// readline decodes the child's bytes as UTF-8 (one U+FFFD per ill-formed subpart) before it splits them.
	lines := readlineLines(jsstring.FromUTF8([]byte(stdout.String())))
	var relativized []string
	for _, raw := range lines {
		line := jsstring.Trim(strings.TrimSuffix(raw, "\r"))
		if line == "" {
			continue
		}
		relativized = append(relativized, relativizeFindResultPath(line, searchPath))
	}
	if waitErr != nil && len(relativized) == 0 {
		msg := jsstring.Trim(stderr.String())
		if msg == "" {
			code := -1
			if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
				code = exitErr.ExitCode()
			}
			msg = fmt.Sprintf("fd exited with code %d", code)
		}
		return findError(msg), nil
	}
	if len(relativized) == 0 {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "No files found matching pattern"}}}, nil
	}
	resultLimitReached := float64(len(relativized)) > effectiveLimit
	if resultLimitReached {
		//portlint:allow numbers len(relativized) > effectiveLimit and effectiveLimit is floored above, so int holds it exactly and slice(0, effectiveLimit) takes the first effectiveLimit results
		relativized = relativized[:int(effectiveLimit)]
	}
	return formatFindResults(relativized, effectiveLimit, resultLimitReached, false), nil
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
	results, err := ops.Glob(pattern, searchPath, FindGlobOptions{Ignore: []string{"**/node_modules/**", "**/.git/**"}, Limit: limit + 1})
	if ctx.Err() != nil {
		return findError("Operation aborted")
	}
	if err != nil {
		return findError(err.Error())
	}
	if len(results) == 0 {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "No files found matching pattern"}}}
	}
	limitReached := false
	// A custom glob receives limit+1; the extra result only reports that the limit was reached.
	if float64(len(results)) > limit {
		//portlint:allow numbers len(results) > limit and limit is the whole number the caller floored, so int holds it exactly and slice(0, limit) takes the first limit results
		results = results[:int(limit)]
		limitReached = true
	}
	relativized := make([]string, len(results))
	for i, result := range results {
		relativized[i] = relativizeFindResultPath(result, searchPath)
	}
	return formatFindResults(relativized, limit, limitReached, true)
}

func formatFindResults(relativized []string, effectiveLimit float64, resultLimitReached, custom bool) agent.AgentToolResult {
	// Number.MAX_SAFE_INTEGER is observable in the truncation details, even though only bytes cap this tool.
	tr := TruncateHead(strings.Join(relativized, "\n"), truncationLimits(DefaultMaxBytes, 1<<53-1))
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
		shareTruncatedContent(details.Truncation, resultOutput)
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

// relativizeFindResultPath mirrors upstream relativizeFindResultPath on the host's path module: an absolute result
// becomes relative to the search root, separators become "/", and a trailing separator (a directory) is kept.
func relativizeFindResultPath(resultPath, searchPath string) string {
	return relativizeFindResultPathFor(resultPath, searchPath, runtime.GOOS == "windows", nodepath.Process())
}

// relativizeFindResultPathFor is relativizeFindResultPath with upstream's injectable pathModule: path.win32 when
// windows is set, else path.posix, each reading the process state env.
// A working directory that cannot be read leaves the result unrelativized.
func relativizeFindResultPathFor(resultPath, searchPath string, windows bool, env nodepath.Env) string {
	sep := "/"
	isAbsolute, relative := nodepath.PosixIsAbsolute, nodepath.PosixRelative
	if windows {
		sep = `\`
		isAbsolute, relative = nodepath.Win32IsAbsolute, nodepath.Win32Relative
	}
	hadTrailingSeparator := strings.HasSuffix(resultPath, sep) || (sep == `\` && strings.HasSuffix(resultPath, "/"))
	relativePath := resultPath
	if isAbsolute(resultPath) {
		if rel, err := relative(env, searchPath, resultPath); err == nil {
			relativePath = rel
		}
	}
	posixPath := strings.Join(strings.Split(relativePath, sep), "/")
	if hadTrailingSeparator && !strings.HasSuffix(posixPath, "/") {
		return posixPath + "/"
	}
	return posixPath
}

// insideGitRepo is the .git lookup above the search path; gitRepoLookup replaces it in tests.
func (t *FindTool) insideGitRepo(searchPath string) bool {
	if t.gitRepoLookup != nil {
		return t.gitRepoLookup(searchPath)
	}
	return insideGitRepo(searchPath)
}

// findFdInvocation is the fd command line of one find call and, on Windows for a pattern with a path separator, the matcher that picks the
// paths fd lists.
type findFdInvocation struct {
	args        []string
	pathMatcher *minimatch.Minimatch
}

// newFindFdInvocation builds find.ts's fd arguments. On Windows `\` in the pattern is a path separator and becomes `/` (#9262); on POSIX it stays
// a glob escape. fd before 10.5.1 never matches --full-path globs on Windows and workarounds for that break on fd 10.5.1 (sharkdp/fd#2067), so on
// Windows a path pattern (one with `/`) is not given to fd: fd lists every entry and the matcher decides (#11972). Its options keep fd's glob
// behaviour: `*` matches dotfiles, `+(a|b)` is literal, and an all-lowercase pattern matches case-insensitively (smart case). A trailing `/**`
// matches as `/**/*`, because minimatch's `src/**` also matches `src` itself and fd's does not.
func newFindFdInvocation(pattern, searchPath string, effectiveLimit float64, windows, insideGit bool) (findFdInvocation, error) {
	args := []string{"--color=never", "--hidden"}
	// fd ignores .gitignore outside git repos unless --no-require-git; inside a repo its git-aware default stops parent rules at nested repos
	// (upstream #5960).
	if !insideGit {
		args = append(args, "--no-require-git")
	}
	// fd --glob matches the basename unless --full-path is set; in --full-path mode it matches the absolute candidate path, so a path-containing
	// pattern needs a leading "**/".
	effectivePattern := pattern
	if windows {
		effectivePattern = strings.ReplaceAll(pattern, `\`, "/")
	}
	// fd's full paths never contain "/./", so "./src/*.ts" would find nothing; drop the leading "./" (upstream #12020).
	for strings.HasPrefix(effectivePattern, "./") {
		//portlint:allow utf16units the two bytes are the ASCII "./" the loop condition matched, so a byte and a UTF-16 offset agree
		effectivePattern = effectivePattern[2:]
	}
	isPathPattern := strings.Contains(effectivePattern, "/")
	if isPathPattern && !strings.HasPrefix(effectivePattern, "/") && !strings.HasPrefix(effectivePattern, "**/") && effectivePattern != "**" {
		effectivePattern = "**/" + effectivePattern
	}
	if windows && isPathPattern {
		globPattern := effectivePattern
		if strings.HasSuffix(globPattern, "/**") {
			globPattern = strings.TrimSuffix(globPattern, "**") + "**/*"
		}
		matcher, err := minimatch.New(globPattern, minimatch.Options{Dot: true, NoExt: true, NoCase: effectivePattern == jsstring.ToLower(effectivePattern), Platform: "win32"})
		if err != nil {
			return findFdInvocation{}, err
		}
		return findFdInvocation{args: append(args, "--", "", searchPath), pathMatcher: matcher}, nil
	}
	// One result past the limit tells "exactly N matches" from "more than N".
	args = append(args, "--glob", "--max-results", jsNumber(effectiveLimit+1))
	if isPathPattern {
		args = append(args, "--full-path")
	}
	return findFdInvocation{args: append(args, "--", effectivePattern, searchPath)}, nil
}

// findPathFilter is the writer fd's stdout goes to when a path matcher decides the results: it keeps the lines the matcher accepts, one past the
// limit at most, and stops fd once it has them.
type findPathFilter struct {
	matcher *minimatch.Minimatch
	limit   float64
	stop    func()

	pending []byte
	count   float64
	done    bool
	// kept is the accepted lines, each followed by a newline.
	kept strings.Builder
}

func (f *findPathFilter) Write(p []byte) (int, error) {
	if f.done {
		return len(p), nil
	}
	f.pending = append(f.pending, p...)
	for !f.done {
		end := bytes.IndexByte(f.pending, '\n')
		if end < 0 {
			break
		}
		f.line(f.pending[:end])
		f.pending = f.pending[end+1:]
	}
	if f.done {
		f.pending = nil
	}
	return len(p), nil
}

// finish handles the text after the last newline, which readline emits as a final line.
func (f *findPathFilter) finish() {
	if !f.done && len(f.pending) > 0 {
		f.line(f.pending)
	}
	f.pending = nil
}

func (f *findPathFilter) line(raw []byte) {
	line := jsstring.Trim(strings.TrimSuffix(jsstring.FromUTF8(raw), "\r"))
	if line == "" || !f.matcher.Match(line) {
		return
	}
	f.kept.WriteString(line)
	f.kept.WriteByte('\n')
	f.count++
	if f.count > f.limit {
		f.done = true
		f.stop()
	}
}
