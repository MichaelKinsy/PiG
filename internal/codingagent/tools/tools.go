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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
	"github.com/MichaelKinsy/PiG/internal/truncate"
)

// upstream: coding-agent/src/core/tools/truncate.ts:DEFAULT_MAX_BYTES
const (
	DefaultMaxBytes = truncate.DefaultMaxBytes
	DefaultMaxLines = truncate.DefaultMaxLines
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
//  2. systemBinaryNames (fd has "fdfind" fallback for Debian/Ubuntu) that
//     upstream's commandExists can spawn: nodespawn.LookPath, which on
//     Windows finds only a .com or .exe file, from the working directory
//     first unless NoDefaultCurrentDirectoryInExePath is set.
//
// Returns "" if nothing is found. On Windows, returns the bare command
// name, as upstream's getToolPath does.
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
		if path, err := nodespawn.LookPath(name, "", nil); err == nil {
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

// ReadToolOptions is upstream's ReadToolOptions (read.ts:63-70).
type ReadToolOptions struct {
	// AutoResizeImages resizes images before they reach the model; nil is true.
	AutoResizeImages *bool
	// ResizeOptions is the fallback resize profile when the execution context has no model metadata.
	ResizeOptions *ai.ModelImageResizeOptions
	// Operations delegates file reads; nil is the local filesystem.
	Operations *ReadOperations
}

// BashToolOptions is upstream's BashToolOptions (bash.ts:214-226).
type BashToolOptions struct {
	// Operations delegates command execution; nil is the local shell.
	Operations BashOperations
	// BinDir is the managed tools directory (Pi's getBinDir()); ToolsOptions.BinDir sets it for the built-in sets.
	BinDir string
	// CommandPrefix is prepended to every command.
	CommandPrefix string
	// ShellPath is an explicit shell path.
	ShellPath string
	// Settings resolves the shell path live when ShellPath is empty. It stands in for Pi's getShellConfig(settingsManager.getShellPath()) read at call time.
	Settings SettingsView
	// ExposeSessionEnvironment exposes the PI_* session variables; nil is true.
	ExposeSessionEnvironment *bool
	// SpawnHook adjusts the command, working directory or environment before execution.
	SpawnHook BashSpawnHook
}

// PowerShellToolOptions is upstream's PowerShellToolOptions: the operations, session-environment and spawn-hook options of BashToolOptions.
type PowerShellToolOptions struct {
	Operations BashOperations
	// BinDir is the managed tools directory (Pi's getBinDir()).
	BinDir                   string
	ExposeSessionEnvironment *bool
	SpawnHook                PowerShellSpawnHook
}

// WriteToolOptions is upstream's WriteToolOptions.
type WriteToolOptions struct{ Operations *WriteOperations }

// EditToolOptions is upstream's EditToolOptions.
type EditToolOptions struct{ Operations *EditOperations }

// GrepToolOptions is upstream's GrepToolOptions.
type GrepToolOptions struct {
	Operations *GrepOperations
	// BinDir is the managed tools directory (Pi's getBinDir()).
	BinDir string
}

// FindToolOptions is upstream's FindToolOptions.
type FindToolOptions struct {
	Operations *FindOperations
	// BinDir is the managed tools directory (Pi's getBinDir()).
	BinDir string
}

// LsToolOptions is upstream's LsToolOptions.
type LsToolOptions struct{ Operations *LsOperations }

// ToolsOptions is upstream's ToolsOptions (tools/index.ts:107-116).
type ToolsOptions struct {
	Read       *ReadToolOptions
	Bash       *BashToolOptions
	Powershell *PowerShellToolOptions
	Write      *WriteToolOptions
	Edit       *EditToolOptions
	Grep       *GrepToolOptions
	Find       *FindToolOptions
	Ls         *LsToolOptions
	// BinDir is <agentDir>/bin: grep and find resolve rg and fd there first, then on PATH, and download them when missing (upstream ensureTool); bash and powershell put it on PATH. Empty means PATH-only lookup and no downloads. It stands in for Pi's process-wide getBinDir().
	BinDir string
}

// ToolsOptionsFromSettings builds the options a session passes for its settings: the command prefix, the live shell path and the image auto-resize flag. agentBinDir is ToolsOptions.BinDir.
func ToolsOptionsFromSettings(settings BashSettingsView, agentBinDir string) *ToolsOptions {
	options := &ToolsOptions{BinDir: agentBinDir}
	if settings == nil {
		return options
	}
	options.Bash = &BashToolOptions{CommandPrefix: settings.GetCommandPrefix(), Settings: settings}
	if images, ok := settings.(interface{ GetImageAutoResize() bool }); ok {
		autoResize := images.GetImageAutoResize()
		options.Read = &ReadToolOptions{AutoResizeImages: &autoResize}
	}
	return options
}

func orEmpty[T any](options *T) T {
	if options == nil {
		return *new(T)
	}
	return *options
}

// CreateReadTool is upstream's createReadTool (read.ts:222).
func CreateReadTool(cwd string, options *ReadToolOptions) *ReadTool {
	o := orEmpty(options)
	return &ReadTool{CWD: cwd, AutoResizeImages: o.AutoResizeImages, ResizeOptions: o.ResizeOptions, Operations: o.Operations}
}

// CreateBashTool is upstream's createBashTool (bash.ts:434).
func CreateBashTool(cwd string, options *BashToolOptions) *BashTool {
	o := orEmpty(options)
	settings := o.Settings
	if o.ShellPath != "" {
		settings = fixedShellPath(o.ShellPath)
	}
	return &BashTool{
		CWD: cwd, Operations: o.Operations, Settings: settings, CommandPrefix: o.CommandPrefix, BinDir: o.BinDir,
		HideSessionEnvironment: o.ExposeSessionEnvironment != nil && !*o.ExposeSessionEnvironment, SpawnHook: o.SpawnHook,
	}
}

// fixedShellPath is a SettingsView with an explicit shell path.
type fixedShellPath string

func (p fixedShellPath) GetShellPath() (string, error) { return string(p), nil }

// CreatePowerShellTool is upstream's createPowerShellTool (powershell.ts:59).
func CreatePowerShellTool(cwd string, options *PowerShellToolOptions) *PowerShellTool {
	o := orEmpty(options)
	return &PowerShellTool{CWD: cwd, Operations: o.Operations, BinDir: o.BinDir, HideSessionEnvironment: o.ExposeSessionEnvironment != nil && !*o.ExposeSessionEnvironment, SpawnHook: o.SpawnHook}
}

// processFileMutationQueue is upstream's module-level fileMutationQueues map (file-mutation-queue.ts:4): every edit and write tool made by the Create functions serialises through it, however many tools or sessions the process holds.
var processFileMutationQueue = NewFileMutationQueue()

// CreateEditTool is upstream's createEditTool (edit.ts:218); its mutations serialise with every other edit and write tool's through the process queue.
func CreateEditTool(cwd string, options *EditToolOptions) *EditTool {
	return &EditTool{CWD: cwd, Queue: processFileMutationQueue, Operations: orEmpty(options).Operations}
}

// CreateWriteTool is upstream's createWriteTool (write.ts:95); its mutations serialise with every other edit and write tool's through the process queue.
func CreateWriteTool(cwd string, options *WriteToolOptions) *WriteTool {
	return &WriteTool{CWD: cwd, Queue: processFileMutationQueue, Operations: orEmpty(options).Operations}
}

// CreateGrepTool is upstream's createGrepTool (grep.ts:321).
func CreateGrepTool(cwd string, options *GrepToolOptions) *GrepTool {
	o := orEmpty(options)
	return &GrepTool{CWD: cwd, Tools: searchToolsManager(o.BinDir), Operations: o.Operations}
}

// CreateFindTool is upstream's createFindTool (find.ts:316).
func CreateFindTool(cwd string, options *FindToolOptions) *FindTool {
	o := orEmpty(options)
	return &FindTool{CWD: cwd, Tools: searchToolsManager(o.BinDir), Operations: o.Operations}
}

// CreateLsTool is upstream's createLsTool (ls.ts:173).
func CreateLsTool(cwd string, options *LsToolOptions) *LsTool {
	return &LsTool{CWD: cwd, Operations: orEmpty(options).Operations}
}

// withOption is a copy of the options with set applied, so the built-in sets can inject ToolsOptions.BinDir without changing the caller's options.
func withOption[T any](p *T, set func(*T)) *T {
	var c T
	if p != nil {
		c = *p
	}
	set(&c)
	return &c
}

// CreateTool is upstream's createTool: the built-in tool with the given name.
func CreateTool(name, cwd string, options *ToolsOptions) (agent.AgentTool, error) {
	o := orEmpty(options)
	switch name {
	case "read":
		return CreateReadTool(cwd, o.Read), nil
	case "bash":
		return CreateBashTool(cwd, withOption(o.Bash, func(b *BashToolOptions) { b.BinDir = o.BinDir })), nil
	case "powershell":
		return CreatePowerShellTool(cwd, withOption(o.Powershell, func(p *PowerShellToolOptions) { p.BinDir = o.BinDir })), nil
	case "edit":
		return CreateEditTool(cwd, o.Edit), nil
	case "write":
		return CreateWriteTool(cwd, o.Write), nil
	case "grep":
		return CreateGrepTool(cwd, withOption(o.Grep, func(g *GrepToolOptions) { g.BinDir = o.BinDir })), nil
	case "find":
		return CreateFindTool(cwd, withOption(o.Find, func(f *FindToolOptions) { f.BinDir = o.BinDir })), nil
	case "ls":
		return CreateLsTool(cwd, o.Ls), nil
	}
	return nil, fmt.Errorf("unknown tool name %q", name)
}

// CreateCodingTools returns the default set of LLM-callable coding tools,
// mirroring upstream createCodingTools (tools/index.ts:195). Edit and write
// serialise concurrent write/edit calls to the same file from parallel tool
// batches through the process-wide mutation queue.
func CreateCodingTools(cwd string, options *ToolsOptions) []agent.AgentTool {
	o := orEmpty(options)
	// Upstream order (Pi 0.87.1 createAllTools): read, bash, edit, write, then grep, find, ls.
	return []agent.AgentTool{
		CreateReadTool(cwd, o.Read),
		CreateBashTool(cwd, withOption(o.Bash, func(b *BashToolOptions) { b.BinDir = o.BinDir })),
		CreateEditTool(cwd, o.Edit),
		CreateWriteTool(cwd, o.Write),
		CreateGrepTool(cwd, withOption(o.Grep, func(g *GrepToolOptions) { g.BinDir = o.BinDir })),
		CreateFindTool(cwd, withOption(o.Find, func(f *FindToolOptions) { f.BinDir = o.BinDir })),
		CreateLsTool(cwd, o.Ls),
	}
}

// CreateAllTools returns every registered built-in tool, mirroring upstream
// createAllTools (tools/index.ts:213): CreateCodingTools plus the opt-in
// powershell tool, in upstream registry order. Callers select the active
// subset with SelectBuiltinTools.
func CreateAllTools(cwd string, options *ToolsOptions) []agent.AgentTool {
	o := orEmpty(options)
	coding := CreateCodingTools(cwd, options)
	all := make([]agent.AgentTool, 0, len(coding)+1)
	all = append(all, coding[:2]...) // read, bash
	all = append(all, CreatePowerShellTool(cwd, withOption(o.Powershell, func(p *PowerShellToolOptions) { p.BinDir = o.BinDir })))
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

// CreateReadOnlyTools returns read-only tools (read, grep, find, ls), mirroring upstream createReadOnlyTools (tools/index.ts:204).
func CreateReadOnlyTools(cwd string, options *ToolsOptions) []agent.AgentTool {
	o := orEmpty(options)
	return []agent.AgentTool{
		CreateReadTool(cwd, o.Read),
		CreateGrepTool(cwd, withOption(o.Grep, func(g *GrepToolOptions) { g.BinDir = o.BinDir })),
		CreateFindTool(cwd, withOption(o.Find, func(f *FindToolOptions) { f.BinDir = o.BinDir })),
		CreateLsTool(cwd, o.Ls),
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
func resolvePath(cwd, path string) (string, error) {
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
