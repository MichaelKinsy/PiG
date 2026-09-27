package codingagent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// PiSourceInfo is upstream's SourceInfo (source-info.ts) as it travels to
// extensions and RPC clients: where a tool, command, template or skill came
// from.
type PiSourceInfo struct {
	Path    string `json:"path"`
	Source  string `json:"source"`
	Scope   string `json:"scope"`
	Origin  string `json:"origin"`
	BaseDir string `json:"baseDir,omitempty"`
}

// PiSlashCommand mirrors upstream SlashCommandInfo (slash-commands.ts), the
// entry type of pi.getCommands() and of RPC get_commands.
type PiSlashCommand struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Source      string       `json:"source"`
	SourceInfo  PiSourceInfo `json:"sourceInfo"`
}

// LlamaExtensionPath is the source path upstream gives the built-in inline
// llama.cpp extension (`<inline:${name}>`).
const LlamaExtensionPath = "<inline:llama.cpp>"

// LlamaSlashCommand is the /llama command of the built-in llama.cpp
// extension, as getCommands lists it.
func LlamaSlashCommand() PiSlashCommand {
	return PiSlashCommand{
		Name: llama.CommandName, Description: llama.CommandDescription, Source: "extension",
		SourceInfo: PiSourceInfo{Path: LlamaExtensionPath, Source: "inline", Scope: "temporary", Origin: "top-level"},
	}
}

// ExtensionCommandLister is the extension runner surface the command catalog
// reads.
type ExtensionCommandLister interface {
	Commands() []extension.ResolvedCommand
}

// SlashCommandCatalog lists the commands upstream AgentSession's getCommands
// (agent-session.ts _bindExtensionCore) and RPC get_commands report:
// extension commands, then prompt templates, then skills.
type SlashCommandCatalog struct {
	Runner ExtensionCommandLister
	// Inline are the commands of upstream's inline built-in extensions
	// (llama.cpp). Upstream loads inline extensions after every path
	// extension (resource-loader.ts loadFinalExtensionSet), so their commands
	// follow the runner's.
	Inline          []PiSlashCommand
	PromptTemplates []PromptTemplate
	Skills          []*SkillDef
	CWD             string
	AgentDir        string
	SourceInfo      map[string]ResourceSourceInfo
}

// Commands returns the catalog in upstream order.
func (c SlashCommandCatalog) Commands() []PiSlashCommand {
	commands := make([]PiSlashCommand, 0)
	if c.Runner != nil {
		for _, command := range c.Runner.Commands() {
			commands = append(commands, PiSlashCommand{
				Name: strings.TrimPrefix(command.InvocationName, "/"), Description: command.Description,
				Source: "extension", SourceInfo: PiSourceInfoValue(command.SourceInfo),
			})
		}
	}
	commands = append(commands, c.Inline...)
	for _, template := range c.PromptTemplates {
		commands = append(commands, PiSlashCommand{
			Name: template.Name, Description: template.Description, Source: "prompt",
			SourceInfo: c.SourceInfoForPath(template.FilePath, "prompts"),
		})
	}
	for _, skill := range c.Skills {
		commands = append(commands, PiSlashCommand{
			Name: "skill:" + skill.Name, Description: skill.Description, Source: "skill",
			SourceInfo: c.SourceInfoForPath(skill.Path, "skills"),
		})
	}
	return commands
}

// SubprocessCommands returns [SlashCommandCatalog.Commands] in the subprocess
// wire shape.
func (c SlashCommandCatalog) SubprocessCommands() []subprocess.CommandInfo {
	commands := c.Commands()
	out := make([]subprocess.CommandInfo, len(commands))
	for i, command := range commands {
		out[i] = subprocess.CommandInfo{Name: command.Name, Description: command.Description, Source: command.Source, SourceInfo: command.SourceInfo}
	}
	return out
}

// SourceInfoForPath returns the SourceInfo of a resource of kind
// ("extensions", "prompts" or "skills") loaded from path.
func (c SlashCommandCatalog) SourceInfoForPath(path, kind string) PiSourceInfo {
	// Upstream findSourceInfoForPath checks the paths extensions discovered
	// first, for the path or any directory above it.
	for current := path; current != ""; {
		if info, ok := c.SourceInfo[current]; ok && strings.HasPrefix(info.Source, "extension:") {
			return PiSourceInfo{Path: path, Source: info.Source, Scope: info.Scope, Origin: info.Origin, BaseDir: info.BaseDir}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	for _, candidate := range []string{path, filepath.Dir(path)} {
		if info, ok := c.SourceInfo[candidate]; ok {
			scope := info.Scope
			if scope == "" {
				scope = "temporary"
			}
			origin := info.Origin
			if origin == "" {
				origin = "top-level"
			}
			source := info.Source
			if source == "" {
				source = "local"
			}
			// Upstream createSourceInfo: the recorded metadata's baseDir, which
			// a settings entry does not have.
			return PiSourceInfo{Path: path, Source: source, Scope: scope, Origin: origin, BaseDir: info.BaseDir}
		}
	}
	info := PiSourceInfo{Path: path, Source: "local", Scope: "temporary", Origin: "top-level"}
	if path == "builtin:piglet" {
		info.Source = "piglet"
		return info
	}
	if path != "" {
		info.BaseDir = filepath.Dir(path)
	}
	userRoot := filepath.Join(c.AgentDir, kind)
	projectRoot := filepath.Join(c.CWD, CONFIG_DIR_NAME, kind)
	switch {
	case resourcePathWithin(path, userRoot):
		info.Scope, info.BaseDir = "user", userRoot
	case resourcePathWithin(path, projectRoot):
		info.Scope, info.BaseDir = "project", projectRoot
	}
	return info
}

// CLISourceName is upstream's source for a resource named on the command
// line (-e, --skill): resource-loader.ts records {source: "cli", scope:
// "temporary", origin: "top-level"} for it.
const CLISourceName = "cli"

// CLISourceInfo is the SourceInfo upstream stamps on a resource named on the
// command line.
func CLISourceInfo(path string) PiSourceInfo {
	return PiSourceInfo{Path: path, Source: CLISourceName, Scope: "temporary", Origin: "top-level"}
}

// PiSourceInfoValue converts an extension's opaque SourceInfo to its wire
// shape.
func PiSourceInfoValue(value extension.SourceInfo) PiSourceInfo {
	if sourceInfo, ok := value.(PiSourceInfo); ok {
		return sourceInfo
	}
	encoded, err := json.Marshal(value)
	if err == nil {
		var sourceInfo PiSourceInfo
		if json.Unmarshal(encoded, &sourceInfo) == nil && sourceInfo.Path != "" {
			return sourceInfo
		}
	}
	return PiSourceInfo{Source: "local", Scope: "temporary", Origin: "top-level"}
}

func resourcePathWithin(path, base string) bool {
	if path == "" || base == "" {
		return false
	}
	ap, err1 := filepath.Abs(path)
	ab, err2 := filepath.Abs(base)
	if err1 != nil || err2 != nil {
		return false
	}
	if ap == ab {
		return true
	}
	return strings.HasPrefix(ap, ab+string(filepath.Separator))
}

// ExtensionToolLister is the extension runner surface [ExtensionToolInfos]
// reads.
type ExtensionToolLister interface {
	Tools() []extension.RegisteredTool
	ToolSourceInfo(toolName string) (extension.SourceInfo, bool)
}

// ExtensionToolInfos mirrors upstream AgentSession.getAllTools
// (agent-session.ts): the session's tool definition registry, not its active
// tools. Every built-in tool the --tools allowlist and --exclude-tools
// denylist admit is listed, active or not, in createAllToolDefinitions order;
// extension tools follow in registration order, first registration winning
// across extensions, and an extension tool named like a built-in takes that
// built-in's place (_refreshToolRegistry). --no-builtin-tools only changes the
// active set, so it lists the built-ins too.
//
// allowed is nil when no allowlist is in force; an empty allowlist admits
// nothing, as --no-tools does.
func ExtensionToolInfos(runner ExtensionToolLister, allowed, excluded map[string]struct{}) []subprocess.ToolInfo {
	admitted := func(name string) bool {
		if allowed != nil {
			if _, ok := allowed[name]; !ok {
				return false
			}
		}
		_, denied := excluded[name]
		return !denied
	}
	out := make([]subprocess.ToolInfo, 0)
	index := make(map[string]int)
	for _, schema := range tools.BuiltinToolSchemas() {
		if !admitted(schema.Name) {
			continue
		}
		parameters, err := json.Marshal(schema.Parameters)
		if err != nil {
			// Built-in schemas are static maps of JSON values.
			panic("marshal built-in tool schema " + schema.Name + ": " + err.Error())
		}
		index[schema.Name] = len(out)
		out = append(out, subprocess.ToolInfo{
			Name: schema.Name, Description: schema.Description, Parameters: parameters,
			PromptGuidelines: schema.PromptGuidelines,
			SourceInfo:       PiSourceInfo{Path: "<builtin:" + schema.Name + ">", Source: "builtin", Scope: "temporary", Origin: "top-level"},
			Source:           "builtin",
		})
	}
	if runner == nil {
		return out
	}
	for _, tool := range runner.Tools() {
		name := tool.Definition.Name
		if !admitted(name) {
			continue
		}
		sourceInfo, _ := runner.ToolSourceInfo(name)
		info := subprocess.ToolInfo{
			Name: name, Description: tool.Definition.Description, Parameters: tool.Definition.Parameters,
			PromptGuidelines: tool.Definition.PromptGuidelines, SourceInfo: PiSourceInfoValue(sourceInfo),
			Source: toolSource(tool),
		}
		if i, ok := index[name]; ok {
			out[i] = info
			continue
		}
		index[name] = len(out)
		out = append(out, info)
	}
	return out
}

// toolSource is a tool's D23 source attribution: its declared source, or the
// registering extension's name, which the loader stamps as SourceInfo.
func toolSource(tool extension.RegisteredTool) string {
	if source, ok := tool.SourceInfo.(string); ok && source != "" {
		return source
	}
	return "builtin"
}
