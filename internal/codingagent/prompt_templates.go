package codingagent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodefs"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
)

// PromptTemplate is one loaded `.md` file.
type PromptTemplate struct {
	Name         string // basename without .md
	Description  string // from frontmatter, or first non-empty body line truncated to 60
	ArgumentHint string // from frontmatter `argument-hint`, optional
	Content      string // body (without frontmatter)
	FilePath     string // absolute path on disk
	Scope        string // "user" | "project" | "extra"
	// SourceInfo is prompt-templates.ts PromptTemplate.sourceInfo, set by a resource loader from the resolved resource's metadata.
	SourceInfo PiSourceInfo
}

// ─── Argument parsing ────────────────────────────────────────────────────────

// ParsePromptArgs splits arguments using ECMAScript whitespace outside single- and double-quoted runs. Empty quoted strings are omitted, and backslashes are literal.
// Upstream: packages/coding-agent/src/core/prompt-templates.ts:24-55.
func ParsePromptArgs(s string) []string {
	args := make([]string, 0, 4)
	var cur strings.Builder
	var quote rune // 0 = not in quotes
	for _, char := range s {
		if quote != 0 {
			if char == quote {
				quote = 0
				continue
			}
			cur.WriteRune(char)
			continue
		}
		if char == '"' || char == '\'' {
			quote = char
			continue
		}
		if isJSWhitespace(char) {
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(char)
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

// Pre-compiled substitution regexes. Mirrors upstream order:
// 1. positional `$1`,`$2`,... (digit immediately after `$`)
// 2. bash-style slice `${@:N}` and `${@:N:L}`
// 3. `$ARGUMENTS` (alias)
// 4. `$@`
// Replacement happens on the template only: argument values are NOT
// re-scanned for `$N` patterns. Upstream rationale (v0.69.0:55-66).
var (
	// reSubstitute matches, in one left-to-right non-overlapping pass:
	//   ${N:-default} ${@:-default} ${ARGUMENTS:-default}  (g1 target, g2 default)
	//   ${@:N} ${@:N:L}                                     (g3 start, g4 length)
	//   $ARGUMENTS $@ $N                                    (g5 simple)
	// A single pass matches upstream substituteArgs: inserted argument values
	// are never re-scanned for further patterns.
	reSubstitute = lazyregexp.New(`\$\{(\d+|ARGUMENTS|@):-([^}]*)\}|\$\{@:(\d+)(?::(\d+))?\}|\$(ARGUMENTS|@|\d+)`)
)

// SubstitutePromptArgs returns content with `$1`, `${1:-default}`, `${@:N:L}`,
// `${@:-default}`, `$ARGUMENTS`, `$@` expanded against args. Mirrors upstream
// `substituteArgs` (core/prompt-templates.ts). A `:-default` value is used when
// the target arg is missing or empty. Substitution runs in a single pass, so
// argument values are NOT re-scanned for `$N`/`$@`/`$ARGUMENTS` patterns.
func SubstitutePromptArgs(content string, args []string) string {
	allArgs := strings.Join(args, " ")
	return reSubstitute.ReplaceAllStringFunc(content, func(match string) string {
		m := reSubstitute.FindStringSubmatch(match)
		defaultTarget, defaultValue := m[1], m[2]
		sliceStart, sliceLength := m[3], m[4]
		simple := m[5]

		if defaultTarget != "" {
			var value string
			if defaultTarget == "@" || defaultTarget == "ARGUMENTS" {
				value = allArgs
			} else if n, err := strconv.Atoi(defaultTarget); err == nil && n >= 1 && n <= len(args) {
				value = args[n-1]
			}
			if value == "" {
				return defaultValue
			}
			return value
		}

		if sliceStart != "" {
			start, _ := strconv.Atoi(sliceStart)
			start-- // 1-indexed -> 0-indexed
			if start < 0 {
				start = 0
			}
			if start >= len(args) {
				return ""
			}
			if sliceLength != "" {
				length, _ := strconv.Atoi(sliceLength)
				// args.slice(start, start + length): a length past the end (up to Atoi's saturated maximum) takes the rest without overflowing.
				end := len(args)
				if length < len(args)-start {
					end = start + length
				}
				return strings.Join(args[start:end], " ")
			}
			return strings.Join(args[start:], " ")
		}

		if simple == "ARGUMENTS" || simple == "@" {
			return allArgs
		}
		if n, err := strconv.Atoi(simple); err == nil && n >= 1 && n <= len(args) {
			return args[n-1]
		}
		return ""
	})
}

// ─── Loader ──────────────────────────────────────────────────────────────────

// LoadPromptTemplatesResult carries loaded commands and file-read or YAML warnings.
type LoadPromptTemplatesResult struct {
	Templates   []PromptTemplate
	Diagnostics []extension.ResourceDiagnostic
}

// LoadPromptTemplatesOptions are upstream's LoadPromptTemplatesOptions.
type LoadPromptTemplatesOptions struct {
	// Cwd is the working directory for project-local templates.
	Cwd string
	// AgentDir is the agent config directory for global templates.
	AgentDir string
	// PromptPaths are explicit prompt template paths (files or directories).
	PromptPaths []string
	// IncludeDefaults includes the default prompt directories.
	IncludeDefaults bool
}

// LoadPromptTemplatesFromOptions loads prompt templates from the global `<agentDir>/prompts/` and project `<cwd>/<config dir>/prompts/` directories when IncludeDefaults is set, then from the explicit paths. Every template is returned, the same name more than once: collisions belong to the resource loader.
//
// upstream: prompt-templates.ts:217-266 (loadPromptTemplates)
func LoadPromptTemplatesFromOptions(options LoadPromptTemplatesOptions) LoadPromptTemplatesResult {
	return loadPromptTemplates(options.Cwd, options.AgentDir, options.IncludeDefaults, options.IncludeDefaults, options.PromptPaths)
}

// LoadPromptTemplates loads the user and project prompt directories when the agent directory and the working directory are given, then the explicit paths, and keeps the first template of each name, reporting the others as collisions as the resource loader does ([DedupePromptTemplates]).
func LoadPromptTemplates(cwd, agentDir string, extraPaths ...string) LoadPromptTemplatesResult {
	loaded := loadPromptTemplates(cwd, agentDir, agentDir != "", cwd != "", extraPaths)
	templates, collisions := DedupePromptTemplates(loaded.Templates)
	return LoadPromptTemplatesResult{Templates: templates, Diagnostics: append(loaded.Diagnostics, collisions...)}
}

// DedupePromptTemplates keeps the first template of each name, in order, and reports each later one as a collision with the first.
//
// upstream: resource-loader.ts:1156-1181 (dedupePrompts)
func DedupePromptTemplates(templates []PromptTemplate) ([]PromptTemplate, []extension.ResourceDiagnostic) {
	var kept []PromptTemplate
	var diagnostics []extension.ResourceDiagnostic
	byName := make(map[string]PromptTemplate, len(templates))
	for _, template := range templates {
		if winner, seen := byName[template.Name]; seen {
			diagnostics = append(diagnostics, extension.ResourceDiagnostic{
				Type: "collision", Message: `name "/` + template.Name + `" collision`, Path: template.FilePath,
				Collision: &extension.ResourceCollision{ResourceType: "prompt", Name: template.Name, WinnerPath: winner.FilePath, LoserPath: template.FilePath},
			})
			continue
		}
		byName[template.Name] = template
		kept = append(kept, template)
	}
	return kept, diagnostics
}

func loadPromptTemplates(cwd, agentDir string, userDefaults, projectDefaults bool, promptPaths []string) LoadPromptTemplatesResult {
	resolvedCwd, resolvedAgentDir := cwd, agentDir
	if cwd != "" {
		resolvedCwd, _ = resolvepath.Resolve(cwd, "")
	}
	if agentDir != "" {
		resolvedAgentDir, _ = resolvepath.Resolve(agentDir, "")
	}
	var result LoadPromptTemplatesResult
	add := func(loaded LoadPromptTemplatesResult) {
		result.Templates = append(result.Templates, loaded.Templates...)
		result.Diagnostics = append(result.Diagnostics, loaded.Diagnostics...)
	}
	globalPromptsDir := ""
	if resolvedAgentDir != "" {
		globalPromptsDir = filepath.Join(resolvedAgentDir, "prompts")
	}
	projectPromptsDir := ""
	if resolvedCwd != "" {
		projectPromptsDir = filepath.Join(resolvedCwd, ConfigDirName(), "prompts")
	}
	sources := promptSourceResolver{global: globalPromptsDir, project: projectPromptsDir}
	if userDefaults && globalPromptsDir != "" {
		add(loadTemplatesFromDir(globalPromptsDir, "user", sources))
	}
	if projectDefaults && projectPromptsDir != "" {
		add(loadTemplatesFromDir(projectPromptsDir, "project", sources))
	}
	for _, rawPath := range promptPaths {
		if rawPath == "" {
			continue
		}
		path, err := resolvepath.ResolveTrimmed(rawPath, resolvedCwd)
		if err != nil {
			path = rawPath
		}
		add(loadTemplatesFromPath(path, "extra", sources))
	}
	return result
}

// promptSourceResolver is getSourceInfo of loadPromptTemplates: a path under the global prompts directory is a user resource, under the project prompts directory a project resource, and anything else a temporary resource based at its own directory.
type promptSourceResolver struct{ global, project string }

func (r promptSourceResolver) sourceInfo(path string) (PiSourceInfo, error) {
	if r.global != "" && isUnderPath(path, r.global) {
		return PiSourceInfo{Path: path, Source: "local", Scope: "user", Origin: "top-level", BaseDir: r.global}, nil
	}
	if r.project != "" && isUnderPath(path, r.project) {
		return PiSourceInfo{Path: path, Source: "local", Scope: "project", Origin: "top-level", BaseDir: r.project}, nil
	}
	baseDir := filepath.Dir(path)
	info, err := os.Stat(path)
	if err != nil {
		return PiSourceInfo{}, err
	}
	if info.IsDir() {
		baseDir = path
	}
	return CreateSyntheticSourceInfo(path, SyntheticSourceInfoOptions{Source: "local", BaseDir: baseDir}), nil
}

func promptWarning(path string, err error) LoadPromptTemplatesResult {
	return LoadPromptTemplatesResult{Diagnostics: []extension.ResourceDiagnostic{{Type: "warning", Message: err.Error(), Path: path}}}
}

func loadTemplatesFromPath(path, scope string, sources promptSourceResolver) LoadPromptTemplatesResult {
	info, err := os.Stat(path)
	if err != nil {
		return LoadPromptTemplatesResult{}
	}
	if info.IsDir() {
		return loadTemplatesFromDir(path, scope, sources)
	}
	if !info.Mode().IsRegular() || !strings.HasSuffix(path, ".md") {
		return LoadPromptTemplatesResult{}
	}
	return loadTemplateFromFile(path, scope, sources)
}

func loadTemplatesFromDir(dir, scope string, sources promptSourceResolver) LoadPromptTemplatesResult {
	var result LoadPromptTemplatesResult
	entries, err := nodefs.ReadDir(dir)
	if err != nil {
		return result
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		loaded := loadTemplateFromFile(path, scope, sources)
		result.Templates = append(result.Templates, loaded.Templates...)
		result.Diagnostics = append(result.Diagnostics, loaded.Diagnostics...)
	}
	return result
}

func parsePromptFrontmatter(content string) (map[string]any, string, error) {
	doc := frontmatter.Parse(content)
	return doc.Frontmatter, doc.Body, doc.Err
}

func loadTemplateFromFile(path, scope string, sources promptSourceResolver) LoadPromptTemplatesResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return promptWarning(path, err)
	}
	fields, body, err := parsePromptFrontmatter(string(data))
	if err != nil {
		return promptWarning(path, err)
	}
	description, _ := fields["description"].(string)
	if description == "" {
		for line := range strings.SplitSeq(body, "\n") {
			if jsTrim(line) != "" {
				description = truncatePromptDescription(line)
				break
			}
		}
	}
	hint, _ := fields["argument-hint"].(string)
	source, err := sources.sourceInfo(path)
	if err != nil {
		return promptWarning(path, err)
	}
	return LoadPromptTemplatesResult{Templates: []PromptTemplate{{Name: strings.TrimSuffix(filepath.Base(path), ".md"), Description: description, ArgumentHint: hint, Content: body, FilePath: path, Scope: scope, SourceInfo: source}}}
}

// truncatePromptDescription is `line.slice(0, 60)` plus "..." when `line.length > 60`: both count UTF-16 code units.
func truncatePromptDescription(line string) string {
	units := utf16.Encode([]rune(line))
	if len(units) <= 60 {
		return line
	}
	return string(utf16.Decode(units[:60])) + "..."
}

// ─── Expansion ───────────────────────────────────────────────────────────────

// ExpandPromptTemplate consumes a /name command separated from its arguments by ECMAScript whitespace. A matching template returns its expanded body and true; a non-command or unknown name returns an empty string and false.
// Upstream: packages/coding-agent/src/core/prompt-templates.ts:318-334.
func ExpandPromptTemplate(line string, templates []PromptTemplate) (string, bool) {
	if !strings.HasPrefix(line, "/") {
		return "", false
	}
	rest := line[1:]
	var name, argsStr string
	if i := strings.IndexFunc(rest, isJSWhitespace); i >= 0 {
		name = rest[:i]
		argsStr = strings.TrimLeftFunc(rest[i:], isJSWhitespace)
	} else {
		name = rest
	}
	for _, t := range templates {
		if t.Name == name {
			return SubstitutePromptArgs(t.Content, ParsePromptArgs(argsStr)), true
		}
	}
	return "", false
}
