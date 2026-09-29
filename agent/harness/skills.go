// Ports packages/agent/src/harness/skills.ts.
package harness

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/internal/ignorerules"
)

// SkillDiagnostic is a warning from skill discovery or metadata validation.
type SkillDiagnostic = PromptTemplateDiagnostic

// SourcedSkill retains the base Skill or application-mapped value with its provenance.
type SourcedSkill[S any] struct {
	Skill  any `json:"skill"`
	Source S   `json:"source"`
}

// SourcedSkillDiagnostic associates a warning with its input provenance.
type SourcedSkillDiagnostic[S any] struct {
	SkillDiagnostic
	Source S `json:"source"`
}

// FormatSkillInvocation wraps a skill with its reference directory and optional additional instructions.
func FormatSkillInvocation(skill Skill, additionalInstructions string) string {
	block := `<skill name="` + skill.Name + `" location="` + skill.FilePath + `">` + "\nReferences are relative to " + dirnameEnvPath(skill.FilePath) + ".\n\n" + skill.Content + "\n</skill>"
	if additionalInstructions != "" {
		block += "\n\n" + additionalInstructions
	}
	return block
}

// LoadSkills recursively discovers skills through the execution environment. SKILL.md takes precedence over descendants, root Markdown files require a description, and failures are returned as warnings without discarding valid siblings.
func LoadSkills(ctx context.Context, env ExecutionEnv, dirs []string) ([]Skill, []SkillDiagnostic) {
	skills := []Skill{}
	diagnostics := []SkillDiagnostic{}
	for _, dir := range dirs {
		info, err := env.FileInfo(ctx, dir)
		if err != nil {
			appendResourceInfoError(&diagnostics, dir, err)
			continue
		}
		if resolveResourceKind(ctx, env, info, &diagnostics) != FileKindDirectory {
			continue
		}
		rules := []ignorerules.Rule{}
		loaded, warnings := loadSkillsDirectory(ctx, env, info.Path, true, &rules, info.Path)
		skills = append(skills, loaded...)
		diagnostics = append(diagnostics, warnings...)
	}
	return skills, diagnostics
}

// LoadSourcedSkills attaches input provenance to every skill and diagnostic. A nil mapper retains the base Skill; a mapper can return an application-specific value and runs in load order with the caller context. A mapper error stops loading and is returned unchanged.
func LoadSourcedSkills[S any](ctx context.Context, env ExecutionEnv, inputs []SourcedPath[S], mapper func(Skill, S, context.Context) (any, error)) ([]SourcedSkill[S], []SourcedSkillDiagnostic[S], error) {
	skills := []SourcedSkill[S]{}
	diagnostics := []SourcedSkillDiagnostic[S]{}
	for _, input := range inputs {
		loaded, warnings := LoadSkills(ctx, env, []string{input.Path})
		for _, skill := range loaded {
			var value any = skill
			if mapper != nil {
				var err error
				value, err = mapper(skill, input.Source, ctx)
				if err != nil {
					return nil, nil, err
				}
			}
			skills = append(skills, SourcedSkill[S]{Skill: value, Source: input.Source})
		}
		for _, warning := range warnings {
			diagnostics = append(diagnostics, SourcedSkillDiagnostic[S]{SkillDiagnostic: warning, Source: input.Source})
		}
	}
	return skills, diagnostics, nil
}

func loadSkillsDirectory(ctx context.Context, env ExecutionEnv, dir string, includeRootFiles bool, rules *[]ignorerules.Rule, root string) ([]Skill, []SkillDiagnostic) {
	skills := []Skill{}
	diagnostics := []SkillDiagnostic{}
	info, err := env.FileInfo(ctx, dir)
	if err != nil {
		appendResourceInfoError(&diagnostics, dir, err)
		return skills, diagnostics
	}
	if resolveResourceKind(ctx, env, info, &diagnostics) != FileKindDirectory {
		return skills, diagnostics
	}
	addSkillIgnoreRules(ctx, env, rules, dir, root, &diagnostics)
	entries, err := env.ListDir(ctx, dir)
	if err != nil {
		return skills, append(diagnostics, resourceWarning("list_failed", dir, err.Error()))
	}
	for _, entry := range entries {
		if entry.Name != "SKILL.md" || resolveResourceKind(ctx, env, entry, &diagnostics) != FileKindFile || resourceIgnored(root, entry.Path, false, *rules) {
			continue
		}
		skill, warnings := loadSkillFile(ctx, env, entry.Path, info.Name)
		if skill != nil {
			skills = append(skills, *skill)
		}
		return skills, append(diagnostics, warnings...)
	}
	sortResourceEntries(entries)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, ".") || entry.Name == "node_modules" {
			continue
		}
		kind := resolveResourceKind(ctx, env, entry, &diagnostics)
		if kind == "" || resourceIgnored(root, entry.Path, kind == FileKindDirectory, *rules) {
			continue
		}
		if kind == FileKindDirectory {
			loaded, warnings := loadSkillsDirectory(ctx, env, entry.Path, false, rules, root)
			skills = append(skills, loaded...)
			diagnostics = append(diagnostics, warnings...)
		} else if kind == FileKindFile && includeRootFiles && strings.HasSuffix(entry.Name, ".md") {
			skill, warnings := loadSkillFile(ctx, env, entry.Path, info.Name)
			if skill != nil {
				skills = append(skills, *skill)
			}
			diagnostics = append(diagnostics, warnings...)
		}
	}
	return skills, diagnostics
}
func resourceIgnored(root, path string, directory bool, rules []ignorerules.Rule) bool {
	return ignorerules.Ignored("/"+relativeEnvPath(root, path), directory, "/", rules)
}
func addSkillIgnoreRules(ctx context.Context, env ExecutionEnv, rules *[]ignorerules.Rule, dir, root string, diagnostics *[]SkillDiagnostic) {
	prefix := relativeEnvPath(root, dir)
	if prefix != "" {
		prefix += "/"
	}
	for _, name := range []string{".gitignore", ".ignore", ".fdignore"} {
		path, err := env.JoinPath(ctx, []string{dir, name})
		if err != nil {
			appendResourceInfoError(diagnostics, dir, err)
			continue
		}
		info, err := env.FileInfo(ctx, path)
		if err != nil {
			appendResourceInfoError(diagnostics, path, err)
			continue
		}
		if info.Kind != FileKindFile {
			continue
		}
		content, err := env.ReadTextFile(ctx, path)
		if err != nil {
			*diagnostics = append(*diagnostics, resourceWarning("read_failed", path, err.Error()))
			continue
		}
		patterns := []string{}
		for line := range strings.SplitSeq(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
			if pattern := prefixSkillIgnorePattern(line, prefix); pattern != "" {
				patterns = append(patterns, pattern)
			}
		}
		*rules = ignorerules.AppendPatterns(*rules, patterns)
	}
}
func prefixSkillIgnorePattern(line, prefix string) string {
	trimmed := trimResourceSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}
	pattern := line
	negated := strings.HasPrefix(pattern, "!")
	if negated || strings.HasPrefix(pattern, `\!`) {
		pattern = pattern[1:]
	}
	pattern = strings.TrimPrefix(pattern, "/")
	pattern = prefix + pattern
	if negated {
		return "!" + pattern
	}
	return pattern
}

var skillNamePattern = lazyregexp.New(`^[a-z0-9-]+$`)

func loadSkillFile(ctx context.Context, env ExecutionEnv, path, parent string) (*Skill, []SkillDiagnostic) {
	diagnostics := []SkillDiagnostic{}
	normalized := strings.TrimRight(path, `\/`)
	nameStart := strings.LastIndexAny(normalized, `\/`) + 1
	declared := normalized[nameStart:] == "SKILL.md"
	content, err := env.ReadTextFile(ctx, path)
	if err != nil {
		return nil, []SkillDiagnostic{resourceWarning("read_failed", path, err.Error())}
	}
	fields, body, err := parseResourceFrontmatter(content)
	if err != nil {
		if declared {
			diagnostics = append(diagnostics, resourceWarning("parse_failed", path, err.Error()))
		}
		return nil, diagnostics
	}
	description, _ := fields["description"].(string)
	if !declared && trimResourceSpace(description) == "" {
		return nil, diagnostics
	}
	add := func(message string) {
		diagnostics = append(diagnostics, resourceWarning("invalid_metadata", path, message))
	}
	if trimResourceSpace(description) == "" {
		add("description is required")
	} else if length := len(utf16.Encode([]rune(description))); length > 1024 {
		add(fmt.Sprintf("description exceeds 1024 characters (%d)", length))
	}
	name, _ := fields["name"].(string)
	if name == "" {
		name = parent
	}
	if name != parent {
		add(fmt.Sprintf("name %q does not match parent directory %q", name, parent))
	}
	if length := len(utf16.Encode([]rune(name))); length > 64 {
		add(fmt.Sprintf("name exceeds 64 characters (%d)", length))
	}
	if !skillNamePattern.MatchString(name) {
		add("name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		add("name must not start or end with a hyphen")
	}
	if strings.Contains(name, "--") {
		add("name must not contain consecutive hyphens")
	}
	if trimResourceSpace(description) == "" {
		return nil, diagnostics
	}
	return &Skill{Name: name, Description: description, Content: body, FilePath: path, DisableModelInvocation: fields["disable-model-invocation"] == true}, diagnostics
}
func dirnameEnvPath(path string) string {
	normalized := strings.TrimRight(path, `\/`)
	index := strings.LastIndexAny(normalized, `\/`)
	if index == 2 && normalized[1] == ':' {
		return normalized[:3]
	}
	if index <= 0 {
		return "/"
	}
	return normalized[:index]
}
func relativeEnvPath(root, path string) string {
	root = strings.TrimRight(strings.ReplaceAll(root, `\`, "/"), "/")
	path = strings.TrimRight(strings.ReplaceAll(path, `\`, "/"), "/")
	if root == path {
		return ""
	}
	if rest, ok := strings.CutPrefix(path, root+"/"); ok {
		return rest
	}
	return strings.TrimLeft(path, "/")
}
