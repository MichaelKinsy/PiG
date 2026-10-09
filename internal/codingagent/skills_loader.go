// Ports packages/coding-agent/src/core/skills.ts.
package codingagent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
	"github.com/MichaelKinsy/PiG/internal/ignorerules"
	"github.com/MichaelKinsy/PiG/internal/nodefs"
)

// LoadSkillsFromDirOptions selects one skill directory and its provenance source.
type LoadSkillsFromDirOptions struct {
	Dir    string
	Source string
}

// LoadSkillsOptions selects default and explicit skill paths in their precedence order.
type LoadSkillsOptions struct {
	CWD             string
	AgentDir        string
	SkillPaths      []string
	IncludeDefaults bool
}

// LoadSkillsResult contains accepted skills and ordered validation/collision diagnostics.
type LoadSkillsResult struct {
	Skills      []*SkillDef
	Diagnostics []extension.ResourceDiagnostic
}

// LoadSkillsFromDir scans direct root Markdown files and nested SKILL.md files. A directory's own SKILL.md takes precedence, and invalid declared skills retain diagnostics while ordinary documentation is ignored.
func LoadSkillsFromDir(options LoadSkillsFromDirOptions) LoadSkillsResult {
	rules := []ignorerules.Rule{}
	return loadValidatedSkillsDir(options.Dir, options.Source, true, &rules, options.Dir)
}
func loadValidatedSkillsDir(dir, source string, rootFiles bool, rules *[]ignorerules.Rule, root string) LoadSkillsResult {
	result := LoadSkillsResult{Skills: []*SkillDef{}, Diagnostics: []extension.ResourceDiagnostic{}}
	*rules = ignorerules.Append(*rules, dir, root)
	entries, err := nodefs.ReadDir(dir)
	if err != nil {
		return result
	}
	add := func(loaded LoadSkillsResult) {
		result.Skills = append(result.Skills, loaded.Skills...)
		result.Diagnostics = append(result.Diagnostics, loaded.Diagnostics...)
	}
	for _, entry := range entries {
		if entry.Name() != "SKILL.md" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || ignorerules.Ignored(path, false, root, *rules) {
			continue
		}
		add(loadValidatedSkillFile(path, source))
		return result
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		if err != nil || ignorerules.Ignored(path, info.IsDir(), root, *rules) {
			continue
		}
		if info.IsDir() {
			add(loadValidatedSkillsDir(path, source, false, rules, root))
		} else if info.Mode().IsRegular() && rootFiles && strings.HasSuffix(entry.Name(), ".md") {
			add(loadValidatedSkillFile(path, source))
		}
	}
	return result
}
func loadValidatedSkillFile(path, source string) LoadSkillsResult {
	result := LoadSkillsResult{Skills: []*SkillDef{}, Diagnostics: []extension.ResourceDiagnostic{}}
	warning := func(message string) {
		result.Diagnostics = append(result.Diagnostics, extension.ResourceDiagnostic{Type: "warning", Message: message, Path: path})
	}
	content, err := os.ReadFile(path)
	if err != nil {
		warning(err.Error())
		return result
	}
	doc := frontmatter.Parse(string(content))
	declared := filepath.Base(path) == "SKILL.md"
	if doc.Err != nil {
		if declared {
			warning(doc.Err.Error())
		}
		return result
	}
	description, _ := doc.Frontmatter["description"].(string)
	if !declared && jsTrim(description) == "" {
		return result
	}
	name, _ := doc.Frontmatter["name"].(string)
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	skill := &SkillDef{Name: name, Description: description, FilePath: path, BaseDir: filepath.Dir(path), Body: doc.Body, DisableModelInvocation: doc.Frontmatter["disable-model-invocation"] == true}
	// Description diagnostics precede name diagnostics in Pi's loader.
	diagnostics := SkillDiagnostics(skill)
	for _, diagnostic := range diagnostics {
		if strings.HasPrefix(diagnostic, "description") {
			warning(diagnostic)
		}
	}
	for _, diagnostic := range diagnostics {
		if !strings.HasPrefix(diagnostic, "description") {
			warning(diagnostic)
		}
	}
	if jsTrim(description) == "" {
		return result
	}
	skill.SourceInfo = CreateSyntheticSourceInfo(path, SyntheticSourceInfoOptions{Source: source, BaseDir: skill.BaseDir})
	switch source {
	case "user", "project":
		skill.SourceInfo.Source = "local"
		skill.SourceInfo.Scope = source
	case "path":
		skill.SourceInfo.Source = "local"
	}
	result.Skills = append(result.Skills, skill)
	return result
}

// LoadSkills resolves explicit paths, loads defaults when requested, and keeps the first public name and physical file. Invalid path syntax returns an error; read/metadata failures and name collisions remain diagnostics.
func LoadSkills(options LoadSkillsOptions) (LoadSkillsResult, error) {
	cwd, err := resolveSkillInputPath(options.CWD, "")
	if err != nil {
		return LoadSkillsResult{}, err
	}
	agentDir, err := resolveSkillInputPath(options.AgentDir, "")
	if err != nil {
		return LoadSkillsResult{}, err
	}
	result := LoadSkillsResult{Skills: []*SkillDef{}, Diagnostics: []extension.ResourceDiagnostic{}}
	collisions := []extension.ResourceDiagnostic{}
	names := map[string]*SkillDef{}
	realPaths := map[string]bool{}
	add := func(loaded LoadSkillsResult) {
		result.Diagnostics = append(result.Diagnostics, loaded.Diagnostics...)
		for _, skill := range loaded.Skills {
			canonical := canonicalizePath(skill.FilePath)
			if realPaths[canonical] {
				continue
			}
			if existing, ok := names[skill.Name]; ok {
				collisions = append(collisions, extension.ResourceDiagnostic{Type: "collision", Message: `name "` + skill.Name + `" collision`, Path: skill.FilePath, Collision: &extension.ResourceCollision{ResourceType: "skill", Name: skill.Name, WinnerPath: existing.FilePath, LoserPath: skill.FilePath}})
			} else {
				names[skill.Name] = skill
				realPaths[canonical] = true
				result.Skills = append(result.Skills, skill)
			}
		}
	}
	userDir := filepath.Join(agentDir, "skills")
	projectDir := filepath.Join(cwd, CONFIG_DIR_NAME, "skills")
	if options.IncludeDefaults {
		add(LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: userDir, Source: "user"}))
		add(LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: projectDir, Source: "project"}))
	}
	for _, raw := range options.SkillPaths {
		path, err := resolveSkillInputPath(jsTrim(raw), cwd)
		if err != nil {
			return LoadSkillsResult{}, err
		}
		info, err := os.Stat(path)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, extension.ResourceDiagnostic{Type: "warning", Message: "skill path does not exist", Path: path})
			continue
		}
		source := "path"
		if !options.IncludeDefaults {
			if resourcePathWithin(path, userDir) {
				source = "user"
			} else if resourcePathWithin(path, projectDir) {
				source = "project"
			}
		}
		switch {
		case info.IsDir():
			add(LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: path, Source: source}))
		case info.Mode().IsRegular() && strings.HasSuffix(path, ".md"):
			add(loadValidatedSkillFile(path, source))
		default:
			result.Diagnostics = append(result.Diagnostics, extension.ResourceDiagnostic{Type: "warning", Message: "skill path is not a markdown file", Path: path})
		}
	}
	result.Diagnostics = append(result.Diagnostics, collisions...)
	return result, nil
}
func resolveSkillInputPath(path, base string) (string, error) {
	return ResolvePath(path, base)
}
