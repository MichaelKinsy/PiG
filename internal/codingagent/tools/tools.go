// Package tools implements the built-in LLM-callable tools.
// Mirrors pi-coding-agent's tools: bash, read, write, edit, grep, find, ls.
//
// fd/rg auto-install: upstream's tools-manager.ts download/extraction
// pipeline is ported in tools_manager.go (ToolsManager.EnsureTool). It
// looks up <agentDir>/bin/<binary> first, then PATH, and downloads from
// GitHub Releases as a last resort. The lookup-only fast path remains
// available via lookupSystemToolPath for callers that don't want to do
// any network work (e.g. early startup).
package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: coding-agent/src/core/tools/truncate.ts:DEFAULT_MAX_BYTES
const (
	DefaultMaxBytes = 50 * 1024 // upstream: packages/coding-agent/src/core/tools/truncate.ts:DEFAULT_MAX_BYTES
	DefaultMaxLines = 2_000     // upstream: packages/coding-agent/src/core/tools/truncate.ts:DEFAULT_MAX_LINES
)

type systemToolConfig struct {
	BinaryName        string
	SystemBinaryNames []string
}

var systemToolConfigs = map[string]systemToolConfig{
	"fd": {
		BinaryName:        "fd",
		SystemBinaryNames: []string{"fd", "fdfind"},
	},
	"rg": {
		BinaryName: "rg",
	},
}

func systemBinaryNames(config systemToolConfig) []string {
	if len(config.SystemBinaryNames) > 0 {
		return config.SystemBinaryNames
	}
	return []string{config.BinaryName}
}

func lookupSystemToolPath(tool string) string {
	return LookupToolPath(tool, "")
}

// LookupToolPath resolves a tool to a usable binary path. Search order:
//  1. agentBinDir (if non-empty): the auto-install location used by
//     ToolsManager.downloadTool.
//  2. exec.LookPath over systemBinaryNames (fd has "fdfind" fallback for
//     Debian/Ubuntu).
//
// Returns "" if nothing is found. On Windows, returns the bare command
// name (matches upstream behavior so cmd.exe expansion works).
func LookupToolPath(tool, agentBinDir string) string {
	config, ok := systemToolConfigs[tool]
	if !ok {
		return ""
	}
	if agentBinDir != "" {
		binExt := ""
		if runtime.GOOS == "windows" {
			binExt = ".exe"
		}
		local := filepath.Join(agentBinDir, config.BinaryName+binExt)
		if _, err := os.Stat(local); err == nil {
			return local
		}
	}
	for _, name := range systemBinaryNames(config) {
		if path, err := exec.LookPath(name); err == nil {
			if runtime.GOOS == "windows" {
				return name
			}
			return path
		}
	}
	return ""
}

// builtinToolNames mirrors upstream allToolNames (tools/index.ts): every
// built-in tool the registry can offer, in createAllTools order.
var builtinToolNames = [...]string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"}

// optInBuiltinToolNames are registered built-ins outside every PiG default
// active set. Upstream registers powershell on every platform, but no default
// selection activates it: a tool allowlist, the defaultTools setting, or an
// explicit active set must name it.
var optInBuiltinToolNames = [...]string{"powershell"}

var builtinToolDescriptions = map[string]string{
	"bash":       "Execute a bash command in the current working directory.",
	"powershell": "Execute a PowerShell command in the current working directory.",
	"read":       "Read the contents of a file.",
	"write":      "Write content to a file.",
	"edit":       "Edit a single file using exact text replacement.",
	"grep":       "Search file contents for a pattern.",
	"find":       "Search for files by glob pattern.",
	"ls":         "List directory contents.",
}

// BuiltinToolNames returns the canonical built-in names in registry order.
// The returned slice is a copy and cannot mutate future host callbacks.
func BuiltinToolNames() []string { return append([]string(nil), builtinToolNames[:]...) }

// BuiltinToolDescription returns short host-action metadata without creating a
// tool instance.
func BuiltinToolDescription(name string) string { return builtinToolDescriptions[name] }

// BuiltinToolActive reports whether the registered built-in name is active.
// A non-nil active set is explicit. A nil active set means PiG's full
// default: every built-in except the opt-in ones, which stay inactive unless
// the allowed tool list names them.
func BuiltinToolActive(name string, active, allowed map[string]struct{}) bool {
	if active != nil {
		_, ok := active[name]
		return ok
	}
	if !slices.Contains(optInBuiltinToolNames[:], name) {
		return true
	}
	_, ok := allowed[name]
	return ok
}

// SelectBuiltinTools keeps the tools BuiltinToolActive reports active.
func SelectBuiltinTools(all []agent.AgentTool, active, allowed map[string]struct{}) []agent.AgentTool {
	out := make([]agent.AgentTool, 0, len(all))
	for _, tool := range all {
		if BuiltinToolActive(tool.Name(), active, allowed) {
			out = append(out, tool)
		}
	}
	return out
}

// ─── All Tools ────────────────────────────────────────────────────────────────

// CreateCodingTools returns the default set of LLM-callable coding tools.
// Edit and write share one FileMutationQueue.
//
// settings is consulted by BashTool to resolve shellPath and
// commandPrefix. nil falls through to the upstream defaults (getShellConfig,
// no prefix).
//
// agentBinDir, when non-empty, is <agentDir>/bin: grep and find resolve rg
// and fd there first, then on PATH, and download them when missing (upstream
// ensureTool); bash puts it on PATH. Pass "" for PATH-only lookup and no
// downloads.
func CreateCodingTools(cwd string, settings BashSettingsView, agentBinDir string) []agent.AgentTool {
	// The shared FileMutationQueue serialises concurrent write/edit
	// calls to the same file (from parallel tool batches in beta.1).
	fmq := NewFileMutationQueue()
	prefix := ""
	if settings != nil {
		prefix = settings.GetCommandPrefix()
	}
	manager := searchToolsManager(agentBinDir)
	// Upstream order (Pi 0.87.1 createAllTools): read, bash, edit, write, then grep, find, ls.
	return []agent.AgentTool{
		readToolWithSettings(cwd, settings),
		&BashTool{CWD: cwd, Settings: settings, CommandPrefix: prefix, BinDir: agentBinDir},
		&EditTool{CWD: cwd, Queue: fmq},
		&WriteTool{CWD: cwd, Queue: fmq},
		&GrepTool{CWD: cwd, Tools: manager},
		&FindTool{CWD: cwd, Tools: manager},
		&LsTool{CWD: cwd},
	}
}

// CreateAllTools returns every registered built-in tool, mirroring upstream
// createAllTools (tools/index.ts): CreateCodingTools plus the opt-in
// powershell tool, in upstream registry order. Callers select the active
// subset with SelectBuiltinTools.
func CreateAllTools(cwd string, settings BashSettingsView, agentBinDir string) []agent.AgentTool {
	coding := CreateCodingTools(cwd, settings, agentBinDir)
	all := make([]agent.AgentTool, 0, len(coding)+1)
	all = append(all, coding[:2]...) // read, bash
	all = append(all, &PowerShellTool{CWD: cwd, BinDir: agentBinDir})
	return append(all, coding[2:]...)
}

// BuiltinToolSchemas returns the schema of every built-in tool, in registry
// order (upstream createAllToolDefinitions), without creating runnable
// tools. The schemas are the definitions upstream getAllTools reports.
func BuiltinToolSchemas() []ai.ToolSchema {
	byName := make(map[string]ai.ToolSchema, len(builtinToolNames))
	for _, t := range builtinSchemaTools() {
		s := t.Schema()
		byName[s.Name] = s
	}
	out := make([]ai.ToolSchema, 0, len(builtinToolNames))
	for _, name := range builtinToolNames {
		out = append(out, byName[name])
	}
	return out
}

func builtinSchemaTools() []agent.AgentTool {
	return []agent.AgentTool{
		&BashTool{}, &PowerShellTool{}, &ReadTool{}, &WriteTool{}, &EditTool{},
		&GrepTool{}, &FindTool{}, &LsTool{},
	}
}

// DefaultToolGuidelines returns the prompt guidelines from each built-in
// tool's Schema(). Used by prompt builders that don't have access to the
// full tool instances (e.g. interactive mode where tools are constructed
// later). Mirrors upstream agent-session.ts:2273-2276.
func DefaultToolGuidelines() map[string][]string {
	m := make(map[string][]string)
	for _, t := range builtinSchemaTools() {
		s := t.Schema()
		if len(s.PromptGuidelines) > 0 {
			m[s.Name] = s.PromptGuidelines
		}
	}
	return m
}

// normalizeForFuzzyMatch applies progressive normalization for fuzzy
// edit matching, mirroring upstream edit-diff.ts normalizeForFuzzyMatch:
//  1. NFKC unicode normalization
//  2. Strip trailing whitespace from each line
//  3. Normalize smart quotes to ASCII equivalents
//  4. Normalize Unicode dashes/hyphens to ASCII hyphen
//  5. Normalize special Unicode spaces to regular space
func normalizeForFuzzyMatch(text string) string {
	// NFKC normalization first (upstream calls .normalize("NFKC")).
	text = norm.NFKC.String(text)

	// Strip trailing whitespace per line.
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	s := strings.Join(lines, "\n")

	replacer := strings.NewReplacer(
		// Smart single quotes -> '
		"\u2018", "'", "\u2019", "'", "\u201A", "'", "\u201B", "'",
		// Smart double quotes -> "
		"\u201C", "\"", "\u201D", "\"", "\u201E", "\"", "\u201F", "\"",
		// Various dashes/hyphens -> -
		"\u2010", "-", "\u2011", "-", "\u2012", "-",
		"\u2013", "-", "\u2014", "-", "\u2015", "-", "\u2212", "-",
		// Special spaces -> regular space (U+00A0, U+2002..U+200A, U+202F, U+205F, U+3000).
		"\u00A0", " ",
		"\u2002", " ", "\u2003", " ", "\u2004", " ", "\u2005", " ",
		"\u2006", " ", "\u2007", " ", "\u2008", " ", "\u2009", " ", "\u200A", " ",
		"\u202F", " ", "\u205F", " ", "\u3000", " ",
	)
	return replacer.Replace(s)
}

// SettingsView (in shell_config.go) is the inner shell-only subset.
type BashSettingsView interface {
	SettingsView
	GetCommandPrefix() string
}

// CreateReadOnlyTools returns read-only tools (read, bash, grep, find, ls).
//
// agentBinDir: see CreateCodingTools for semantics.
func CreateReadOnlyTools(cwd string, settings BashSettingsView, agentBinDir string) []agent.AgentTool {
	prefix := ""
	if settings != nil {
		prefix = settings.GetCommandPrefix()
	}
	manager := searchToolsManager(agentBinDir)
	return []agent.AgentTool{
		readToolWithSettings(cwd, settings),
		&BashTool{CWD: cwd, Settings: settings, CommandPrefix: prefix, BinDir: agentBinDir},
		&GrepTool{CWD: cwd, Tools: manager},
		&FindTool{CWD: cwd, Tools: manager},
		&LsTool{CWD: cwd},
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// searchToolsManager returns the manager grep and find share for
// <agentDir>/bin, or nil for PATH-only lookup.
func searchToolsManager(agentBinDir string) *ToolsManager {
	if agentBinDir == "" {
		return nil
	}
	return NewToolsManager(filepath.Dir(agentBinDir))
}

// ensureSearchTool mirrors upstream grep/find's per-call ensureTool: a
// pinned path wins; otherwise the manager looks in <agentDir>/bin and on
// PATH and downloads a missing tool (silently, as grep and find pass no
// status callback). Without a manager only PATH is searched.
func ensureSearchTool(ctx context.Context, pinned string, manager *ToolsManager, tool string) string {
	if pinned != "" {
		return pinned
	}
	if manager == nil {
		return LookupToolPath(tool, "")
	}
	return manager.EnsureTool(ctx, tool, nil)
}

// resolvePath is the standard path resolution for write/edit/grep/find/ls.
// Delegates to resolveToCwd for ~ expansion + unicode normalization.
//
// upstream: path-utils.ts resolveToCwd
func resolvePath(cwd, path string) string {
	return resolveToCwd(path, cwd)
}

// base64Encode encodes raw bytes to standard base64.
func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Ensure interface implementation
var _ io.Reader = (*bytes.Buffer)(nil)
var _ agent.AgentTool = (*BashTool)(nil)
var _ agent.AgentTool = (*PowerShellTool)(nil)
var _ agent.AgentTool = (*ReadTool)(nil)
var _ agent.AgentTool = (*WriteTool)(nil)
var _ agent.AgentTool = (*EditTool)(nil)
var _ agent.AgentTool = (*GrepTool)(nil)
var _ agent.AgentTool = (*FindTool)(nil)
var _ agent.AgentTool = (*LsTool)(nil)
