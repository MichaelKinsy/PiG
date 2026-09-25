package codingagent

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// PromptTemplate is one loaded `.md` file.
type PromptTemplate struct {
	Name         string // basename without .md
	Description  string // from frontmatter, or first non-empty body line truncated to 60
	ArgumentHint string // from frontmatter `argument-hint`, optional
	Content      string // body (without frontmatter)
	FilePath     string // absolute path on disk
	Scope        string // "user" | "project" | "external"
}

// ─── Argument parsing ────────────────────────────────────────────────────────

// ParsePromptArgs splits an argument string into tokens, respecting
// single- and double-quoted runs. Whitespace outside quotes is the
// separator. Mirrors upstream `parseCommandArgs`
// (core/prompt-templates.ts v0.69.0:24-52).
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
		if unicode.IsSpace(char) {
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
	reSubstitute = regexp.MustCompile(`\$\{(\d+|ARGUMENTS|@):-([^}]*)\}|\$\{@:(\d+)(?::(\d+))?\}|\$(ARGUMENTS|@|\d+)`)
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
				end := min(start+length, len(args))
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

// LoadPromptTemplates loads user, project, and explicit prompt paths.
func LoadPromptTemplates(cwd, agentDir string, extraPaths ...string) LoadPromptTemplatesResult {
	var result LoadPromptTemplatesResult
	var all []PromptTemplate
	add := func(r LoadPromptTemplatesResult) {
		all = append(all, r.Templates...)
		result.Diagnostics = append(result.Diagnostics, r.Diagnostics...)
	}
	if agentDir != "" {
		userDir := filepath.Join(agentDir, "prompts")
		add(loadTemplatesFromDir(userDir, "user"))
	}
	if cwd != "" {
		projectRoot := ProjectConfigDir(cwd)
		absoluteProjectRoot, projectErr := filepath.Abs(projectRoot)
		absoluteConfigRoot, configErr := filepath.Abs(ConfigRoot())
		if projectErr != nil || configErr != nil || absoluteProjectRoot != absoluteConfigRoot {
			add(loadTemplatesFromDir(filepath.Join(projectRoot, "prompts"), "project"))
		}
	}
	for _, path := range extraPaths {
		if path != "" {
			add(loadTemplatesFromPath(path, "extra"))
		}
	}
	// First-wins by name, matching resource-loader's ordered path precedence.
	byName := make(map[string]PromptTemplate, len(all))
	order := make([]string, 0, len(all))
	for _, template := range all {
		if winner, seen := byName[template.Name]; seen {
			result.Diagnostics = append(result.Diagnostics, extension.ResourceDiagnostic{
				Type: "collision", Message: `name "/` + template.Name + `" collision`, Path: template.FilePath,
				Collision: &extension.ResourceCollision{ResourceType: "prompt", Name: template.Name, WinnerPath: winner.FilePath, LoserPath: template.FilePath},
			})
			continue
		}
		order = append(order, template.Name)
		byName[template.Name] = template
	}
	out := make([]PromptTemplate, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	result.Templates = out
	return result
}

func promptWarning(path string, err error) LoadPromptTemplatesResult {
	return LoadPromptTemplatesResult{Diagnostics: []extension.ResourceDiagnostic{{Type: "warning", Message: err.Error(), Path: path}}}
}

func loadTemplatesFromPath(path, scope string) LoadPromptTemplatesResult {
	info, err := os.Stat(path)
	if err != nil {
		return LoadPromptTemplatesResult{}
	}
	if info.IsDir() {
		return loadTemplatesFromDir(path, scope)
	}
	if !info.Mode().IsRegular() || !strings.HasSuffix(path, ".md") {
		return LoadPromptTemplatesResult{}
	}
	return loadTemplateFromFile(path, scope)
}

func loadTemplatesFromDir(dir, scope string) LoadPromptTemplatesResult {
	var result LoadPromptTemplatesResult
	entries, err := os.ReadDir(dir)
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
		loaded := loadTemplateFromFile(path, scope)
		result.Templates = append(result.Templates, loaded.Templates...)
		result.Diagnostics = append(result.Diagnostics, loaded.Diagnostics...)
	}
	return result
}

func parsePromptFrontmatter(content string) (map[string]any, string, error) {
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text.StripBom(content))
	if !strings.HasPrefix(normalized, "---") {
		return nil, normalized, nil
	}
	end := strings.Index(normalized[3:], "\n---")
	if end < 0 {
		return nil, normalized, nil
	}
	end += 3
	body := strings.TrimSpace(normalized[end+4:])
	var parsed any
	if end > 4 {
		if err := yaml.Unmarshal([]byte(normalized[4:end]), &parsed); err != nil {
			return nil, "", err
		}
	}
	fields, _ := parsed.(map[string]any)
	return fields, body, nil
}

func loadTemplateFromFile(path, scope string) LoadPromptTemplatesResult {
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
			if strings.TrimSpace(line) != "" {
				description = truncatePromptDescription(line)
				break
			}
		}
	}
	hint, _ := fields["argument-hint"].(string)
	return LoadPromptTemplatesResult{Templates: []PromptTemplate{{Name: strings.TrimSuffix(filepath.Base(path), ".md"), Description: description, ArgumentHint: hint, Content: body, FilePath: path, Scope: scope}}}
}

func truncatePromptDescription(line string) string {
	runes := []rune(line)
	if len(runes) <= 60 {
		return line
	}
	return string(runes[:60]) + "..."
}

// ─── Expansion ───────────────────────────────────────────────────────────────

// ExpandPromptTemplate consumes a `/name [args]` line. If `name`
// matches a loaded template, returns the expanded body with arg
// substitution applied and ok=true. Otherwise returns ("", false).
// Lines without a leading `/` always return ("", false). Mirrors
// upstream `expandPromptTemplate` (v0.69.0:293-308).
func ExpandPromptTemplate(line string, templates []PromptTemplate) (string, bool) {
	if !strings.HasPrefix(line, "/") {
		return "", false
	}
	rest := line[1:]
	var name, argsStr string
	if i := strings.IndexFunc(rest, unicode.IsSpace); i >= 0 {
		name = rest[:i]
		argsStr = strings.TrimLeftFunc(rest[i+1:], unicode.IsSpace)
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
