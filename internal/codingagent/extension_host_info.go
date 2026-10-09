package codingagent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// PiSourceInfo is upstream's SourceInfo (source-info.ts) as it travels to
// extensions and RPC clients: where a tool, command, template or skill came
// from.
type PiSourceInfo = extension.SourceInfo

// SlashCommandInfo mirrors upstream SlashCommandInfo (core/slash-commands.ts), the
// entry type of pi.getCommands() and of RPC get_commands.
type SlashCommandInfo = extension.SlashCommandInfo

// LlamaExtensionPath is the source path upstream gives the built-in llama.cpp
// extension (`builtin:${name}`).
// Ports .upstream/v0.99.1/packages/coding-agent/src/extensions/index.ts:8 and core/resource-loader.ts:706-735.
const LlamaExtensionPath = BuiltinPathPrefix + "llama.cpp"

// ExtensionCommandLister is the extension runner surface the command catalog
// reads.
type ExtensionCommandLister interface {
	Commands() []extension.ResolvedCommand
}

// SlashCommandCatalog lists the commands upstream AgentSession's getCommands
// (agent-session.ts _bindExtensionCore) and RPC get_commands report:
// extension commands, then prompt templates, then skills.
type SlashCommandCatalog struct {
	Runner          ExtensionCommandLister
	PromptTemplates []PromptTemplate
	Skills          []*SkillDef
	CWD             string
	AgentDir        string
	SourceInfo      map[string]ResourceSourceInfo
}

// Commands returns the catalog in upstream order.
func (c SlashCommandCatalog) Commands() []SlashCommandInfo {
	commands := make([]SlashCommandInfo, 0)
	if c.Runner != nil {
		for _, command := range c.Runner.Commands() {
			commands = append(commands, SlashCommandInfo{
				Name: strings.TrimPrefix(command.InvocationName, "/"), Description: command.Description,
				Source: string(SlashSourceExtension), SourceInfo: PiSourceInfoValue(command.SourceInfo),
			})
		}
	}
	for _, template := range c.PromptTemplates {
		commands = append(commands, SlashCommandInfo{
			Name: template.Name, Description: template.Description, Source: string(SlashSourcePrompt),
			SourceInfo: c.resourceSourceInfo(template.SourceInfo, template.FilePath, "prompts"),
		})
	}
	for _, skill := range c.Skills {
		commands = append(commands, SlashCommandInfo{
			Name: "skill:" + skill.Name, Description: skill.Description, Source: string(SlashSourceSkill),
			SourceInfo: c.resourceSourceInfo(skill.SourceInfo, skill.FilePath, "skills"),
		})
	}
	return commands
}

// resourceSourceInfo is the sourceInfo a prompt template or skill carries (agent-session.ts getCommands reads template.sourceInfo and skill.sourceInfo). A resource a mode loaded without one gets the provenance resolved from its path.
func (c SlashCommandCatalog) resourceSourceInfo(own PiSourceInfo, path, kind string) PiSourceInfo {
	// upstream: resource-loader.ts:880-882 (updatePromptsFromPaths) gives a prompt findSourceInfoForPath(filePath) before the
	// source info it was loaded with, so a path an extension or the package resolver recorded beats the loader's default.
	if info, found := c.recordedSourceInfo(path); found {
		return info
	}
	if own != (PiSourceInfo{}) {
		return own
	}
	return c.defaultSourceInfo(path, kind)
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

// WithSkillSources projects loaded skills with the same resource provenance used by command discovery. Inline skills retain their authored metadata; file-backed skills use resolver and extension-discovery metadata.
// Ports packages/coding-agent/src/core/resource-loader.ts
func (c SlashCommandCatalog) WithSkillSources(skills []*SkillDef) []*SkillDef {
	out := make([]*SkillDef, 0, len(skills))
	for _, skill := range skills {
		copy := *skill
		if skill.FilePath != "" && skill.SourceInfo.Source != "inline" {
			copy.SourceInfo = c.SourceInfoForPath(skill.FilePath, "skills")
		}
		out = append(out, &copy)
	}
	return out
}

// SourceInfoForPath returns the SourceInfo of a resource of kind
// ("extensions", "prompts" or "skills") loaded from path.
func (c SlashCommandCatalog) SourceInfoForPath(path, kind string) PiSourceInfo {
	if info, found := c.recordedSourceInfo(path); found {
		return info
	}
	return c.defaultSourceInfo(path, kind)
}

// recordedSourceInfo is resource-loader.ts findSourceInfoForPath: the SourceInfo extension discovery or the package resolver recorded for path, if any.
func (c SlashCommandCatalog) recordedSourceInfo(path string) (PiSourceInfo, bool) {
	// Upstream findSourceInfoForPath checks the paths extensions discovered
	// first, for the path or any directory above it.
	for current := path; current != ""; {
		if info, ok := c.SourceInfo[current]; ok && strings.HasPrefix(info.Source, "extension:") {
			return PiSourceInfo{Path: path, Source: info.Source, Scope: info.Scope, Origin: info.Origin, BaseDir: info.BaseDir}, true
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
			return PiSourceInfo{Path: path, Source: source, Scope: scope, Origin: origin, BaseDir: info.BaseDir}, true
		}
	}
	return PiSourceInfo{}, false
}

// defaultSourceInfo is resource-loader.ts getDefaultSourceInfoForPath: a user or project resource by directory, else a temporary one.
func (c SlashCommandCatalog) defaultSourceInfo(path, kind string) PiSourceInfo {
	info := CreateSyntheticSourceInfo(path, SyntheticSourceInfoOptions{Source: "local"})
	if path == "builtin:piglet" {
		info.Source = "piglet"
		return info
	}
	if path != "" {
		info.BaseDir = filepath.Dir(path)
	}
	userRoot := filepath.Join(c.AgentDir, kind)
	projectRoot := filepath.Join(ProjectConfigDir(c.CWD), kind)
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
	return CreateSyntheticSourceInfo(path, SyntheticSourceInfoOptions{Source: CLISourceName})
}

// SyntheticSourceInfoOptions is the options object of upstream's createSyntheticSourceInfo; an empty Scope or Origin takes upstream's default.
type SyntheticSourceInfoOptions struct {
	Source  string
	Scope   string
	Origin  string
	BaseDir string
}

// CreateSyntheticSourceInfo is upstream's createSyntheticSourceInfo (source-info.ts:41): a SourceInfo for a resource without a file source, with scope "temporary" and origin "top-level" unless the options name others.
func CreateSyntheticSourceInfo(path string, options SyntheticSourceInfoOptions) PiSourceInfo {
	info := PiSourceInfo{Path: path, Source: options.Source, Scope: options.Scope, Origin: options.Origin, BaseDir: options.BaseDir}
	if info.Scope == "" {
		info.Scope = "temporary"
	}
	if info.Origin == "" {
		info.Origin = "top-level"
	}
	return info
}

// PiSourceInfoValue is the wire value of an extension's SourceInfo: the value itself, or upstream's local temporary default when the source has no path.
func PiSourceInfoValue(value extension.SourceInfo) PiSourceInfo {
	if value.Path != "" {
		return value
	}
	return PiSourceInfo{Source: "local", Scope: "temporary", Origin: "top-level"}
}

func resourcePathWithin(path, base string) bool {
	if path == "" || base == "" {
		return false
	}
	ap, err1 := nodepath.Resolve(path)
	ab, err2 := nodepath.Resolve(base)
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
	// Entries are names or `*` patterns, and MCP tools stay registered unless the allowlist filters them (agent-session.ts _isAllowedTool).
	admitted := extension.NewToolFilter(allowed, excluded).Allows
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
			SourceInfo:       CreateSyntheticSourceInfo(BuiltinPathPrefix+schema.Name, SyntheticSourceInfoOptions{Source: "builtin"}),
			Exposure:         extension.ToolExposureDirect,
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
			Exposure: tool.Definition.Exposure,
			Source:   toolSource(tool),
		}
		if info.Exposure == "" {
			info.Exposure = extension.ToolExposureDirect
		}
		if tool.Definition.Namespace != nil {
			info.Namespace = new(*tool.Definition.Namespace)
		}
		if tool.Definition.Annotations != nil {
			info.Annotations = new(*tool.Definition.Annotations)
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
	if tool.Source != "" {
		return tool.Source
	}
	return "builtin"
}
