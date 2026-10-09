package codingagent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"golang.org/x/text/encoding/unicode"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// SkillFrontmatter is upstream's SkillFrontmatter (core/skills.ts:67): the parsed header of a skill file. The three named fields have typed accessors; every other key stays in the map.
type SkillFrontmatter map[string]any

// Name is the skill's `name`, or "" when absent.
func (f SkillFrontmatter) Name() string { return frontmatter.Doc{Frontmatter: f}.String("name") }

// Description is the skill's `description`, or "" when absent.
func (f SkillFrontmatter) Description() string {
	return frontmatter.Doc{Frontmatter: f}.String("description")
}

// DisableModelInvocation is the skill's `disable-model-invocation` flag.
func (f SkillFrontmatter) DisableModelInvocation() bool {
	return frontmatter.Doc{Frontmatter: f}.Bool("disable-model-invocation")
}

// ParseSkillFrontmatter splits a skill file into its header and trimmed body, as upstream's parseFrontmatter<SkillFrontmatter> does.
func ParseSkillFrontmatter(content string) (SkillFrontmatter, string, error) {
	doc := frontmatter.Parse(content)
	if doc.Err != nil {
		return nil, "", doc.Err
	}
	return SkillFrontmatter(doc.Frontmatter), doc.Body, nil
}

// SkillDef is a parsed Markdown skill with frontmatter and body.
//
// Pig loads these on `--skill <name>` and appends the body
// to the system prompt under a `## Skills` heading.
type SkillDef struct {
	SourceInfo             PiSourceInfo
	Name                   string
	Description            string
	DisableModelInvocation bool
	// Body is the markdown content (stripped of frontmatter).
	Body string
	// FilePath is the source Markdown file.
	FilePath string
	// BaseDir is the skill's containing directory: used so callers can
	// resolve referenced sibling files.
	BaseDir string
}

// LoadSkill loads a skill by name from `<skillsDir>/<name>/SKILL.md`.
//
// Lookup order (mirrors upstream resource-loader's user-vs-project precedence):
//  1. <pig-config>/skills/<name>/SKILL.md
//  2. <skillsDir>/<name>/SKILL.md  (caller-provided fallback root)
//
// Most callers pass DefaultAgentDir()/skills as skillsDir so the Pig agent tree
// is searched first; we keep the parameter so SDK consumers can point
// at a vendored skills tree without env juggling.
func LoadSkill(skillsDir, name string) (*SkillDef, error) {
	if name == "" {
		return nil, errors.New("skill: empty name")
	}
	path := filepath.Join(skillsDir, name, "SKILL.md")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("skill: %q not found at %s: %w", name, path, fs.ErrNotExist)
	}
	if err != nil {
		return nil, fmt.Errorf("skill: read %s: %w", path, err)
	}
	front, body, err := ParseSkillFrontmatter(string(data))
	if err != nil {
		return nil, fmt.Errorf("skill: parse %s: %w", path, err)
	}
	return &SkillDef{
		Name:                   firstNonEmpty(front.Name(), name),
		Description:            front.Description(),
		DisableModelInvocation: front.DisableModelInvocation(),
		Body:                   jsTrim(body),
		FilePath:               path,
		BaseDir:                filepath.Dir(path),
	}, nil
}

var skillNamePattern = lazyregexp.New(`^[a-z0-9-]+$`)

// SkillDiagnostics validates a skill against the Agent Skills metadata rules.
// A missing description prevents model discovery; other findings are warnings.
func SkillDiagnostics(skill *SkillDef) []string {
	if skill == nil {
		return nil
	}
	var diagnostics []string
	nameLength := len(utf16.Encode([]rune(skill.Name)))
	if nameLength > 64 {
		diagnostics = append(diagnostics, fmt.Sprintf("name exceeds 64 characters (%d)", nameLength))
	}
	if !skillNamePattern.MatchString(skill.Name) {
		diagnostics = append(diagnostics, "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)")
	}
	if strings.HasPrefix(skill.Name, "-") || strings.HasSuffix(skill.Name, "-") {
		diagnostics = append(diagnostics, "name must not start or end with a hyphen")
	}
	if strings.Contains(skill.Name, "--") {
		diagnostics = append(diagnostics, "name must not contain consecutive hyphens")
	}
	descriptionLength := len(utf16.Encode([]rune(skill.Description)))
	if jsTrim(skill.Description) == "" {
		diagnostics = append(diagnostics, "description is required")
	} else if descriptionLength > 1024 {
		diagnostics = append(diagnostics, fmt.Sprintf("description exceeds 1024 characters (%d)", descriptionLength))
	}
	return diagnostics
}

// DeduplicateSkills keeps the first definition for each public name, matching
// Pi's collision behavior after Resource paths are ordered by precedence.
func DeduplicateSkills(defs []*SkillDef) []*SkillDef {
	result, _ := DeduplicateSkillsWithDiagnostics(defs)
	return result
}

// DeduplicateSkillsWithDiagnostics keeps the first name, silently ignores
// repeated real paths, and reports later same-name definitions in input order.
// Ports packages/coding-agent/src/core/skills.ts
func DeduplicateSkillsWithDiagnostics(defs []*SkillDef) ([]*SkillDef, []extension.ResourceDiagnostic) {
	result := make([]*SkillDef, 0, len(defs))
	seen := make(map[string]*SkillDef, len(defs))
	realPaths := make(map[string]bool, len(defs))
	var diagnostics []extension.ResourceDiagnostic
	for _, def := range defs {
		realPath := canonicalizePath(def.FilePath)
		if def.FilePath != "" && realPaths[realPath] {
			continue
		}
		if winner, exists := seen[def.Name]; exists {
			diagnostics = append(diagnostics, extension.ResourceDiagnostic{
				Type: extension.DiagnosticCollision, Message: fmt.Sprintf(`name "%s" collision`, def.Name), Path: def.FilePath,
				Collision: &extension.ResourceCollision{ResourceType: "skill", Name: def.Name, WinnerPath: winner.FilePath, LoserPath: def.FilePath},
			})
			continue
		}
		seen[def.Name] = def
		realPaths[realPath] = true
		result = append(result, def)
	}
	return result, diagnostics
}

// LoadSkillPath loads a skill from a SKILL.md file path or a skill directory.
func LoadSkillPath(path string) (*SkillDef, error) {
	if path == "" {
		return nil, errors.New("skill: empty path")
	}
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("skill: stat %s: %w", path, err)
	}
	if stat.IsDir() {
		path = filepath.Join(path, "SKILL.md")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skill: read %s: %w", path, err)
	}
	front, body, err := ParseSkillFrontmatter(string(data))
	if err != nil {
		return nil, fmt.Errorf("skill: parse %s: %w", path, err)
	}
	name := front.Name()
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	return &SkillDef{
		Name:                   name,
		Description:            front.Description(),
		DisableModelInvocation: front.DisableModelInvocation(),
		Body:                   jsTrim(body),
		FilePath:               path,
		BaseDir:                filepath.Dir(path),
	}, nil
}

// LoadSkillsFromPath loads one or more skills from a path in discovery order, without sorting by frontmatter name.
// Supported forms:
//   - /path/to/skill/SKILL.md
//   - /path/to/skill-dir/          (contains SKILL.md)
//   - /path/to/skills-root/        (contains child dirs with SKILL.md)
func LoadSkillsFromPath(path string) ([]*SkillDef, error) {
	if path == "" {
		return nil, errors.New("skill: empty path")
	}
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("skill: stat %s: %w", path, err)
	}
	if !stat.IsDir() {
		if !strings.HasSuffix(path, ".md") {
			return nil, fmt.Errorf("skill: %s: skill path is not a markdown file", path)
		}
		skill, err := loadSkillFile(path)
		if err != nil || skill == nil {
			return nil, err
		}
		return []*SkillDef{skill}, nil
	}
	if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err == nil {
		skill, err := LoadSkillPath(path)
		if err != nil {
			return nil, err
		}
		return []*SkillDef{skill}, nil
	}
	var out []*SkillDef
	var loadErrors []error
	seen := make(map[string]struct{})
	for _, skillPath := range packageSkillPaths(path) {
		skill, err := loadSkillFile(skillPath)
		if err != nil {
			loadErrors = append(loadErrors, err)
			continue
		}
		if skill == nil {
			continue
		}
		canonical := canonicalizePath(skill.FilePath)
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, skill)
	}
	return out, errors.Join(loadErrors...)
}

// loadSkillFile loads the skill at path, a skill directory or Markdown file,
// as upstream loadSkillFromFile (core/skills.ts) does: a Markdown file other
// than SKILL.md is a skill only when its frontmatter parses and has a
// description, and is otherwise skipped, returning nil without an error.
func loadSkillFile(path string) (*SkillDef, error) {
	skill, err := LoadSkillPath(path)
	file := packagecontent.SkillFile(path)
	if filepath.Base(file) == "SKILL.md" {
		return skill, err
	}
	if err != nil {
		// A file that cannot be read is still reported; one whose
		// frontmatter does not parse is not a skill.
		if _, readErr := os.ReadFile(file); readErr != nil {
			return nil, err
		}
		return nil, nil
	}
	if jsTrim(skill.Description) == "" {
		return nil, nil
	}
	return skill, nil
}

func packageSkillPaths(root string) []string {
	// Keep discovery in packagecontent as the one owner for Package, settings,
	// CLI, and direct SDK resource paths.
	return packagecontent.DiscoverSkillDirs(root)
}

func canonicalizePath(path string) string {
	return CanonicalizePath(path)
}

// DefaultSkillsDir returns the user agent's skills directory.
func DefaultSkillsDir() string {
	return filepath.Join(DefaultAgentDir(), "skills")
}

// ExpandSkillCommand reads the selected skill at invocation time, strips frontmatter, and appends trimmed arguments. Unknown skills are not expanded. Read and parse errors leave input unchanged and carry a skill_expansion diagnostic.
// Ports packages/coding-agent/src/core/agent-session.ts (_expandSkillCommand).
func ExpandSkillCommand(text string, skills []*SkillDef) (string, bool, *extension.ExtensionError) {
	if !strings.HasPrefix(text, "/skill:") {
		return "", false, nil
	}
	rest := text[len("/skill:"):]
	skillName, args, _ := strings.Cut(rest, " ")
	args = jsTrim(args)

	var skill *SkillDef
	for _, s := range skills {
		if s.Name == skillName {
			skill = s
			break
		}
	}
	if skill == nil {
		return "", false, nil
	}
	// pig additive (D18): an inline Piglet skill uses its supplied body; its source path may identify the defining manifest rather than a Markdown file.
	body := skill.Body
	if skill.FilePath != "" && skill.SourceInfo.Source != "inline" {
		content, err := os.ReadFile(skill.FilePath)
		if err != nil {
			operation, path := "open", skill.FilePath
			if pathError, ok := errors.AsType[*os.PathError](err); ok && pathError.Op == "read" {
				operation, path = "read", ""
			}
			return "", false, &extension.ExtensionError{ExtensionPath: skill.FilePath, Event: "skill_expansion", Error: tools.NodeFSError(err, operation, path)}
		}
		decoded, err := unicode.UTF8.NewDecoder().String(string(content))
		if err != nil {
			return "", false, &extension.ExtensionError{ExtensionPath: skill.FilePath, Event: "skill_expansion", Error: err.Error()}
		}
		stripped, err := frontmatter.Strip(decoded)
		if err != nil {
			return "", false, &extension.ExtensionError{ExtensionPath: skill.FilePath, Event: "skill_expansion", Error: err.Error()}
		}
		body = jsTrim(stripped)
	}
	block := "<skill name=\"" + skill.Name + "\" location=\"" + skill.FilePath + "\">\n" +
		"References are relative to " + skill.BaseDir + ".\n\n" +
		body + "\n</skill>"
	if args != "" {
		return block + "\n\n" + args, true, nil
	}
	return block, true, nil
}

// skillBlockRe matches upstream's parseSkillBlock regex:
//
//	/^<skill name="([^"]+)" location="([^"]+)">\n([\s\S]*?)\n<\/skill>(?:\n\n([\s\S]+))?$/
var skillBlockRe = lazyregexp.New(
	`^<skill name="([^"]+)" location="([^"]+)">\n([\s\S]*?)\n</skill>(?:\n\n([\s\S]+))?$`,
)

// ParsedSkillBlockFromText holds the parsed skill invocation data extracted
// from a user message text. Mirrors upstream's ParsedSkillBlock return value
// from agent-session.ts:parseSkillBlock.
type ParsedSkillBlockFromText struct {
	Name        string
	Location    string
	Content     string
	UserMessage string // trailing user text after </skill>\n\n, or ""
}

// ParseSkillBlock attempts to parse a skill XML block from message text.
// Returns nil if the text doesn't match the skill block format.
// Mirrors upstream parseSkillBlock (agent-session.ts:102-116).
func ParseSkillBlock(text string) *ParsedSkillBlockFromText {
	m := skillBlockRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	userMsg := ""
	if len(m) > 4 {
		userMsg = jsTrim(m[4])
	}
	return &ParsedSkillBlockFromText{
		Name:        m[1],
		Location:    m[2],
		Content:     m[3],
		UserMessage: userMsg,
	}
}
