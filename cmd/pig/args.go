package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ─── CLI Flags ────────────────────────────────────────────────────────────────

// CLIFlags holds parsed command-line arguments.
// Mirrors upstream cli/args.ts:parseArgs.
type CLIFlags struct {
	Model        string
	Print        string
	NoExtensions bool
	Extensions   []string
	Skills       []string
	Version      bool
	Help         bool
	// Session selection flags.
	ResumeAny bool // --resume always opens the startup picker; use --session for path or ID
	Continue  bool // --continue: load most recent session non-interactively
	// Remaining args (the prompt in interactive mode)
	Args []string
	// Mode selects the output mode: text (default), json, or rpc.
	// Mirrors upstream --mode.
	Mode string
	// modeSet preserves an explicit --mode text for metadata stdout routing.
	modeSet bool
	// Verbose forces verbose startup output (overrides quietStartup setting).
	// Mirrors upstream --verbose (args.ts:153-154, 239).
	Verbose bool

	// ── Upstream parity flags (args.ts) ──────────────────────────────

	// Provider overrides the default provider name (e.g. "openai", "anthropic").
	// Mirrors upstream --provider (args.ts:79).
	Provider string
	// SystemPrompt selects explicit prompt text or a file. An explicitly empty option suppresses SYSTEM.md discovery and retains the built-in prompt.
	SystemPrompt    string
	systemPromptSet bool
	// AppendSystemPrompt appends text to the system prompt (repeatable).
	// Mirrors upstream --append-system-prompt (args.ts:86).
	AppendSystemPrompt []string
	// Thinking sets the thinking level: off, minimal, low, medium, high, xhigh, max.
	// Mirrors upstream --thinking (args.ts:118).
	Thinking string
	// NoSession disables session persistence (ephemeral).
	// Mirrors upstream --no-session (args.ts:92).
	NoSession bool
	// Session specifies a session file path or partial UUID.
	// Mirrors upstream --session (args.ts:93).
	Session string
	// Fork forks a specific session into a new session.
	// Mirrors upstream --fork (args.ts:94).
	Fork string
	// SessionDir overrides the session storage directory.
	// Mirrors upstream --session-dir (args.ts:95).
	SessionDir string
	// SessionID specifies an exact session ID to use or create.
	// Mirrors upstream --session-id (args.ts:107, v0.76.0).
	SessionID          string
	sessionCwdOverride *string
	// Name is the raw Session display name from --name / -n.
	Name string
	// NameSet distinguishes an omitted --name from an explicitly empty value.
	NameSet bool
	// NoTools disables all tools.
	// Mirrors upstream --no-tools / -nt (args.ts:98).
	NoTools bool
	// NoBuiltinTools disables built-in tools while leaving extension/custom tools available.
	// Mirrors upstream --no-builtin-tools / -nbt (args.ts:100 in v0.70.0).
	NoBuiltinTools bool
	// Tools is a comma-separated allowlist of tool names.
	// Mirrors upstream --tools / -t (args.ts:99).
	Tools []string
	// ExcludeTools is a comma-separated list of tool names to exclude.
	// Mirrors upstream --exclude-tools / -xt (args.ts, v0.77.0).
	ExcludeTools []string
	// ListModels prints available models and exits. Optional search pattern.
	// Mirrors upstream --list-models (args.ts:147).
	ListModels    string
	ListModelsAll bool // --list-models without a nonempty filter
	// NoSkills disables skill discovery.
	// Mirrors upstream --no-skills (args.ts:107).
	NoSkills bool
	// PromptTemplates lists prompt template paths (repeatable).
	// Mirrors upstream --prompt-template (args.ts:110).
	PromptTemplates []string
	// NoPromptTemplates disables prompt template discovery.
	// Mirrors upstream --no-prompt-templates (args.ts:113).
	NoPromptTemplates bool
	// Themes lists theme paths (repeatable).
	// Mirrors upstream --theme (args.ts:114).
	Themes []string
	// NoThemes disables theme discovery.
	// Mirrors upstream --no-themes (args.ts:115).
	NoThemes bool
	// NoContextFiles disables AGENTS.md/CLAUDE.md discovery.
	// Mirrors upstream --no-context-files (args.ts:116).
	NoContextFiles bool
	// Export exports a session file to HTML and exits.
	// Mirrors upstream --export (args.ts:106).
	Export string
	// APIKey overrides the API key.
	// Mirrors upstream --api-key (args.ts:82).
	APIKey string
	// Offline disables startup network operations.
	// Mirrors upstream --offline (args.ts:150).
	Offline bool
	// ProjectTrustOverride controls project-local resource loading for this run.
	// nil uses saved/default trust; true is --approve; false is --no-approve.
	ProjectTrustOverride *bool
	// FileArgs holds @file arguments (files included in prompt).
	// Mirrors upstream args.fileArgs (args.ts:160).
	FileArgs []string
	// Models comma-separated model patterns for Ctrl+P cycling.
	// Mirrors upstream --models (args.ts:96).
	Models []string
	// UnknownFlags preserves flags not claimed by the core parser so
	// extensions can consume them later.
	UnknownFlags map[string]any
	// UseTheme and TuiMode override the theme and TUI mode for this run. UseTheme is nil when omitted; an explicitly empty name is a selection.
	UseTheme *string
	TuiMode  string
	// Diagnostics mirrors Pi parseArgs diagnostics. Any error exits before
	// version or mode handling.
	Diagnostics []argDiagnostic
}

// validThinkingLevels matches upstream VALID_THINKING_LEVELS (args.ts:49).
var validThinkingLevels = map[string]bool{
	"off": true, "minimal": true, "low": true,
	"medium": true, "high": true, "xhigh": true, "max": true,
}

// parseFlags parses os.Args[1:] into CLIFlags.
// Mirrors upstream cli/args.ts:parseArgs.
// argDiagnostic is one Pi parseArgs diagnostic. Type is "warning" or "error".
type argDiagnostic struct {
	Type    string
	Message string
}

// reportArgDiagnostics writes diagnostics like Pi main.ts: red "Error: " and
// yellow "Warning: " lines on stderr, colored only for a terminal. It reports
// whether any diagnostic is an error, which exits with status 1.
func reportArgDiagnostics(w io.Writer, diagnostics []argDiagnostic, color bool) bool {
	hasError := false
	for _, diagnostic := range diagnostics {
		label, sgr := "Warning: ", "\x1b[33m"
		if diagnostic.Type == "error" {
			writeCLIError(w, diagnostic.Message, color)
			hasError = true
			continue
		}
		text := label + diagnostic.Message
		if color {
			text = sgr + text + "\x1b[39m"
		}
		_, _ = fmt.Fprintln(w, text) // Best effort: stderr has no fallback channel.
	}
	return hasError
}

// sessionNameFromFlags preserves omission and applies JavaScript name normalization only when the selected CLI route needs a Session.
func sessionNameFromFlags(flags CLIFlags) (string, error) {
	if flags.Name == "" && !flags.NameSet {
		return "", nil
	}
	name := widthx.JSTrim(flags.Name)
	if name == "" {
		return "", errors.New("--name requires a non-empty value")
	}
	return name, nil
}

// parseFlags parses CLI options, treating arguments after -- as messages or @files.
func parseFlags(args []string) CLIFlags {
	flags := CLIFlags{Mode: "text", UnknownFlags: map[string]any{}}
	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "--":
			for _, positionalArg := range args[i+1:] {
				if path, ok := strings.CutPrefix(positionalArg, "@"); ok {
					flags.FileArgs = append(flags.FileArgs, path)
				} else {
					flags.Args = append(flags.Args, positionalArg)
				}
			}
			return flags
		case arg == "--version" || arg == "-v":
			flags.Version = true
		case arg == "--help" || arg == "-h":
			flags.Help = true
		case arg == "--no-extensions" || arg == "-ne":
			flags.NoExtensions = true
		case arg == "--model" && i+1 < len(args):
			i++
			flags.Model = args[i]
		case arg == "--provider" && i+1 < len(args):
			i++
			flags.Provider = args[i]
		case arg == "--api-key" && i+1 < len(args):
			i++
			flags.APIKey = args[i]
		case arg == "--system-prompt" && i+1 < len(args):
			i++
			flags.SystemPrompt = args[i]
			flags.systemPromptSet = true
		case arg == "--append-system-prompt" && i+1 < len(args):
			i++
			flags.AppendSystemPrompt = append(flags.AppendSystemPrompt, args[i])
		case arg == "--thinking" && i+1 < len(args):
			i++
			level := args[i]
			if validThinkingLevels[level] {
				flags.Thinking = level
			} else {
				flags.Diagnostics = append(flags.Diagnostics, argDiagnostic{Type: "warning", Message: fmt.Sprintf("Invalid thinking level \"%s\". Valid values: off, minimal, low, medium, high, xhigh, max", level)})
			}
		case arg == "--print" || arg == "-p":
			flags.Print = " " // sentinel: print mode enabled
			// Upstream v0.73.1: -p may optionally consume the next positional
			// arg as the prompt (e.g. `pig -p "my prompt"`).
			// Matches args.ts: if next arg exists, isn't an @file, and isn't
			// a flag (unless it starts with "---"), consume it as a message.
			if i+1 < len(args) {
				next := args[i+1]
				if !strings.HasPrefix(next, "@") && (!strings.HasPrefix(next, "-") || strings.HasPrefix(next, "---")) {
					i++
					flags.Args = append(flags.Args, args[i])
				}
			}
		case arg == "--continue" || arg == "-c":
			flags.Continue = true
		case arg == "--resume" || arg == "-r":
			flags.ResumeAny = true
		case arg == "--no-session":
			flags.NoSession = true
		case arg == "--session" && i+1 < len(args):
			i++
			flags.Session = args[i]
		case arg == "--fork" && i+1 < len(args):
			i++
			flags.Fork = args[i]
		case arg == "--session-dir" && i+1 < len(args):
			i++
			flags.SessionDir = args[i]
		case arg == "--session-id" && i+1 < len(args):
			i++
			flags.SessionID = args[i]
		case arg == "--name" || arg == "-n":
			if i+1 < len(args) {
				i++
				flags.Name = args[i]
				flags.NameSet = true
			} else {
				flags.Diagnostics = append(flags.Diagnostics, argDiagnostic{Type: "error", Message: "--name requires a value"})
			}
		case arg == "--no-tools" || arg == "-nt":
			flags.NoTools = true
		case arg == "--no-builtin-tools" || arg == "-nbt":
			flags.NoBuiltinTools = true
		case (arg == "--tools" || arg == "-t") && i+1 < len(args):
			i++
			for t := range strings.SplitSeq(args[i], ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					flags.Tools = append(flags.Tools, t)
				}
			}
		case (arg == "--exclude-tools" || arg == "-xt") && i+1 < len(args):
			i++
			for t := range strings.SplitSeq(args[i], ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					flags.ExcludeTools = append(flags.ExcludeTools, t)
				}
			}
		case arg == "--models" && i+1 < len(args):
			i++
			for m := range strings.SplitSeq(args[i], ",") {
				m = strings.TrimSpace(m)
				if m != "" {
					flags.Models = append(flags.Models, m)
				}
			}
		case (arg == "--extension" || arg == "-e") && i+1 < len(args):
			i++
			flags.Extensions = append(flags.Extensions, args[i])
		case arg == "--skill" && i+1 < len(args):
			i++
			flags.Skills = append(flags.Skills, args[i])
		case arg == "--no-skills" || arg == "-ns":
			flags.NoSkills = true
		case arg == "--prompt-template" && i+1 < len(args):
			i++
			flags.PromptTemplates = append(flags.PromptTemplates, args[i])
		case arg == "--no-prompt-templates" || arg == "-np":
			flags.NoPromptTemplates = true
		case arg == "--theme" && i+1 < len(args):
			i++
			flags.Themes = append(flags.Themes, args[i])
		case arg == "--no-themes":
			flags.NoThemes = true
		case arg == "--no-context-files" || arg == "-nc":
			flags.NoContextFiles = true
		case arg == "--export" && i+1 < len(args):
			i++
			flags.Export = args[i]
		case arg == "--list-models":
			// Optional search pattern (not a flag or @file).
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !strings.HasPrefix(args[i+1], "@") {
				i++
				flags.ListModels = args[i]
				flags.ListModelsAll = flags.ListModels == ""
			} else {
				flags.ListModels = ""
				flags.ListModelsAll = true
			}
		case arg == "--offline":
			flags.Offline = true
		case arg == "--approve" || arg == "-a":
			flags.ProjectTrustOverride = new(true)
		case arg == "--no-approve" || arg == "-na":
			flags.ProjectTrustOverride = new(false)
		case arg == "--use-theme":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				flags.Diagnostics = append(flags.Diagnostics, argDiagnostic{Type: "error", Message: "--use-theme requires a theme name"})
			} else {
				i++
				flags.UseTheme = new(args[i])
			}
		case arg == "--tui-mode":
			switch {
			case i+1 < len(args) && (args[i+1] == "regular" || args[i+1] == "fullscreen"):
				i++
				flags.TuiMode = args[i]
			case i+1 >= len(args) || strings.HasPrefix(args[i+1], "-"):
				flags.Diagnostics = append(flags.Diagnostics, argDiagnostic{Type: "error", Message: "--tui-mode requires regular or fullscreen"})
			default:
				i++
				flags.Diagnostics = append(flags.Diagnostics, argDiagnostic{Type: "error", Message: fmt.Sprintf("Invalid TUI mode \"%s\". Valid values: regular, fullscreen", args[i])})
			}
		case arg == "--mode":
			switch {
			case i+1 >= len(args) || strings.HasPrefix(args[i+1], "-"):
				flags.Diagnostics = append(flags.Diagnostics, argDiagnostic{Type: "error", Message: "--mode requires text, json, or rpc"})
			case args[i+1] == "text" || args[i+1] == "json" || args[i+1] == "rpc":
				i++
				flags.Mode = args[i]
				flags.modeSet = true
			default:
				i++
				flags.Diagnostics = append(flags.Diagnostics, argDiagnostic{Type: "error", Message: fmt.Sprintf("Invalid mode \"%s\". Valid values: text, json, rpc", args[i])})
			}
		case arg == "--verbose":
			flags.Verbose = true
		case strings.HasPrefix(arg, "@"):
			flags.FileArgs = append(flags.FileArgs, strings.TrimPrefix(arg, "@"))
		case strings.HasPrefix(arg, "--"):
			name := strings.TrimPrefix(arg, "--")
			if before, after, ok := strings.Cut(name, "="); ok {
				flags.UnknownFlags[before] = after
			} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !strings.HasPrefix(args[i+1], "@") {
				i++
				flags.UnknownFlags[name] = args[i]
			} else {
				flags.UnknownFlags[name] = true
			}
		case strings.HasPrefix(arg, "-"):
			flags.Diagnostics = append(flags.Diagnostics, argDiagnostic{Type: "error", Message: "Unknown option: " + arg})
		default:
			flags.Args = append(flags.Args, arg)
		}
		i++
	}
	return flags
}

//go:embed help_upstream.txt
var upstreamHelp string

// pigCommands are PiG's additive top-level commands. Pi's own commands are in
// upstreamHelp.
const pigCommands = `PiG Commands:
  pig piglet <command>          List, show, validate, and build Piglets (run without arguments for usage)
  pig extension init <path>     Create a Go, Python, or Rust extension
  pig setup [go|container]      Show or install the toolchains that extensions and Piglet builds use
  pig verify [path...]          Verify this binary, downloads, installed Packages, and Resources
  pig status [--json]           Show Resources, Packages, and Piglets
  pig docs [show <topic>]       Read the local PiG documentation
  pig version                   Show PiG, pinned Pi, Go, and platform versions

PiG Options:
  --piglet <name|path>           Run the named Piglet, or the Piglet file at path

PiG keeps its configuration in ~/.pig and never reads or writes ~/.pi.

`

// extensionFlagsNote is the help paragraph Pi's printHelp follows with the "Extension CLI Flags:" section (args.ts:333).
const extensionFlagsNote = "Extensions can register additional flags (e.g., --plan from plan-mode extension).\n\n"

// printHelp prints Pi's --help rendered with PiG's identity
// (automation/gen/gen-help.sh), with PiG's commands after Pi's. Section headers are
// bold on a terminal, as chalk.bold renders them. Registered extension flags
// follow the extension note, as upstream printHelp(extensionFlags) lists them.
func printHelp(w io.Writer, color bool, extensionFlags ...extension.ExtensionFlag) {
	sections := strings.SplitAfter(upstreamHelp, "\n\n")
	var out strings.Builder
	for _, section := range sections {
		if section == extensionFlagsNote && len(extensionFlags) > 0 {
			out.WriteString(strings.TrimSuffix(section, "\n"))
			out.WriteString(extensionFlagsHelp(extensionFlags))
			out.WriteString("\n\n")
			continue
		}
		out.WriteString(section)
		if strings.HasPrefix(section, "Commands:\n") {
			out.WriteString(pigCommands)
		}
	}
	text := out.String()
	if color {
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			if helpHeader.MatchString(line) {
				lines[i] = "\x1b[1m" + line + "\x1b[22m"
			}
		}
		if name, rest, ok := strings.Cut(lines[0], " - "); ok {
			lines[0] = "\x1b[1m" + name + "\x1b[22m - " + rest
		}
		text = strings.Join(lines, "\n")
	}
	_, _ = io.WriteString(w, text)
}

var helpHeader = lazyregexp.New(`^[A-Z][A-Za-z ]*:$`)

// extensionFlagsHelp ports upstream printHelp's extensionFlagsText (args.ts:262-271): the flag column pads to 30 UTF-16 units without truncation, and a flag without a description names its extension.
func extensionFlagsHelp(flags []extension.ExtensionFlag) string {
	var out strings.Builder
	out.WriteString("Extension CLI Flags:\n")
	for i, flag := range flags {
		if i > 0 {
			out.WriteByte('\n')
		}
		name := "  --" + flag.Name
		if flag.Type == extension.FlagString {
			name += " <value>"
		}
		out.WriteString(name)
		out.WriteString(strings.Repeat(" ", max(0, 30-len(jsstring.ToUTF16(name)))))
		if flag.Description != "" {
			out.WriteString(flag.Description)
		} else {
			out.WriteString("Registered by " + flag.ExtensionPath)
		}
	}
	out.WriteByte('\n')
	return out.String()
}

// extensionHelpFlags lists registered flags as upstream main.ts collects them for printHelp: extensions in load order, then each extension's flags in first-registration order.
func extensionHelpFlags(exts []extension.Extension) []extension.ExtensionFlag {
	var flags []extension.ExtensionFlag
	for _, ext := range exts {
		names := slices.Clone(ext.FlagOrder)
		for _, name := range slices.Sorted(maps.Keys(ext.Flags)) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		for _, name := range names {
			if flag, ok := ext.Flags[name]; ok {
				flags = append(flags, flag)
			}
		}
	}
	return flags
}

// validateForkFlags mirrors upstream main.ts validateForkFlags: --fork
// cannot be combined with --session, --continue, --resume or --no-session.
func validateForkFlags(flags CLIFlags) []argDiagnostic {
	if flags.Fork == "" {
		return nil
	}
	var conflicts []string
	if flags.Session != "" {
		conflicts = append(conflicts, "--session")
	}
	if flags.Continue {
		conflicts = append(conflicts, "--continue")
	}
	if flags.ResumeAny {
		conflicts = append(conflicts, "--resume")
	}
	if flags.NoSession {
		conflicts = append(conflicts, "--no-session")
	}
	if len(conflicts) == 0 {
		return nil
	}
	return []argDiagnostic{{Type: "error", Message: "--fork cannot be combined with " + strings.Join(conflicts, ", ")}}
}
