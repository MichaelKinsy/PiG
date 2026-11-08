// Skill frontmatter/prompt conventions adapted from earendil-works/pi (MIT), as apps/p2/plugins/prompt/context-files.ts
// ports them. See third-party/pi.LICENSE. This host uses only global and cwd-local skill roots.
package prompt

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Resources are a session's instructions and skills sections, every skill (including those hidden from the model with
// `disable-model-invocation`, which `/skill:` still invokes, as in pi) and the prompt templates.
type Resources struct {
	Agents     string
	Skills     string
	SkillFiles []SkillFile
	Templates  []Template
}

// LoadedPrompt is a session's loaded prompt resources.
type LoadedPrompt struct {
	Resources
	Fingerprint string
	// Project is the same without the global `AGENTS.md`, skills and prompt templates (only the project's): what a locked
	// session gets unless its owner picked the global instructions.
	Project *Resources
}

type skill struct {
	name, description, path string
	disabled, global        bool
}

// Entry is a directory entry, without following symbolic links.
type Entry struct {
	Name string
	// Kind is "file", "directory" or "other".
	Kind string
}

// Files is where project instructions and skills are read: the local filesystem, or a session's execution environment.
// Missing files and directories are not found; other failures are errors.
type Files interface {
	Paths() PathRules
	// Local is whether these are this machine's files (the data directory's global `AGENTS.md` is then read once).
	Local() bool
	ReadText(ctx context.Context, path string) (text string, found bool, err error)
	Entries(ctx context.Context, path string) (entries []Entry, found bool, err error)
}

type localFiles struct{}

// LocalFiles is the local filesystem.
var LocalFiles Files = localFiles{}

func (localFiles) Paths() PathRules { return LocalPaths }
func (localFiles) Local() bool      { return true }

func (localFiles) ReadText(_ context.Context, path string) (string, bool, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.New("Could not read resource file")
	}
	return jsstring.FromUTF8(content), true, nil
}

func (localFiles) Entries(_ context.Context, path string) ([]Entry, bool, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("Could not scan skills directory")
	}
	found := make([]Entry, len(entries))
	for at, entry := range entries {
		kind := "other"
		switch {
		case entry.Type().IsRegular():
			kind = "file"
		case entry.IsDir():
			kind = "directory"
		}
		found[at] = Entry{Name: entry.Name(), Kind: kind}
	}
	return found, true, nil
}

func escapeXML(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;").Replace(text)
}

func byName(a, b Entry) int { return p2runtime.LocaleCompare(a.Name, b.Name) }

// skillFiles is pi's discovery (coding-agent core/skills.ts `loadSkillsFromDir`): a directory holding `SKILL.md` is one
// skill and is not searched further; otherwise `.md` files directly in the skills root are skills, and subdirectories are
// searched for `SKILL.md`. Hidden entries and `node_modules` are skipped. Unlike pi, symbolic links are not followed (the
// watch does not follow them either, and there is no cycle to guard against) and `.gitignore`-style files are not read.
func skillFiles(ctx context.Context, files Files, path string, root bool) ([]string, error) {
	entries, found, err := files.Entries(ctx, path)
	if err != nil || !found {
		return nil, err
	}
	if slices.ContainsFunc(entries, func(entry Entry) bool { return entry.Kind == "file" && entry.Name == "SKILL.md" }) {
		return []string{files.Paths().Join(path, "SKILL.md")}, nil
	}
	entries = slices.DeleteFunc(slices.Clone(entries), func(entry Entry) bool {
		return strings.HasPrefix(entry.Name, ".") || entry.Name == "node_modules"
	})
	slices.SortStableFunc(entries, byName)
	var all []string
	for _, entry := range entries {
		child := files.Paths().Join(path, entry.Name)
		switch {
		case entry.Kind == "directory":
			found, err := skillFiles(ctx, files, child, false)
			if err != nil {
				return nil, err
			}
			all = append(all, found...)
		case root && entry.Kind == "file" && strings.HasSuffix(entry.Name, ".md"):
			all = append(all, child)
		}
	}
	return all, nil
}

// AgentsCandidates is each `AGENTS.md` candidate from the filesystem root down to `cwd`.
func AgentsCandidates(paths PathRules, cwd string) ([]string, error) {
	start, err := paths.Resolve(cwd)
	if err != nil {
		return nil, err
	}
	var ancestors []string
	for path := start; ; path = paths.Dirname(path) {
		ancestors = append(ancestors, path)
		if path == paths.Dirname(path) {
			break
		}
	}
	slices.Reverse(ancestors)
	candidates := make([]string, len(ancestors))
	for at, path := range ancestors {
		candidates[at] = paths.Join(path, "AGENTS.md")
	}
	return candidates, nil
}

type instruction struct {
	text   string
	global bool
}

// LoadPromptResources loads the instructions and skills for a session: the global `AGENTS.md` of the data directory
// (always local), then the project's `AGENTS.md` files and skills under skillsRoot (default `<directory>/skills`) and
// `<cwd>/.pi/skills` through files (nil: the local filesystem), and the prompt templates under promptsRoot (default
// `<directory>/prompts`) and `<cwd>/.pi/prompts`.
func LoadPromptResources(ctx context.Context, directory, cwd string, files Files, skillsRoot, promptsRoot string) (*LoadedPrompt, error) {
	if files == nil {
		files = LocalFiles
	}
	if skillsRoot == "" {
		skillsRoot = LocalPaths.Join(directory, "skills")
	}
	if promptsRoot == "" {
		promptsRoot = LocalPaths.Join(directory, "prompts")
	}
	var agents []instruction
	var fingerprint []string
	read := func(path string, from Files, global bool) error {
		content, found, err := from.ReadText(ctx, path)
		if err != nil {
			return err
		}
		fingerprint = append(fingerprint, path+"\n"+content)
		if found {
			agents = append(agents, instruction{text: "## " + path + "\n" + p2runtime.StripBOM(content), global: global})
		}
		return nil
	}
	global := LocalPaths.Join(directory, "AGENTS.md")
	if err := read(global, LocalFiles, true); err != nil {
		return nil, err
	}
	// The same path on a remote environment is another file: only a local session skips the global one it read.
	candidates, err := AgentsCandidates(files.Paths(), cwd)
	if err != nil {
		return nil, err
	}
	for _, path := range candidates {
		if !files.Local() || path != global {
			if err := read(path, files, false); err != nil {
				return nil, err
			}
		}
	}
	var order []string
	skills := map[string]*skill{}
	globalSkills, err := skillFiles(ctx, files, skillsRoot, true)
	if err != nil {
		return nil, err
	}
	projectSkills, err := skillFiles(ctx, files, files.Paths().Join(cwd, ".pi", "skills"), true)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range append(slices.Clone(globalSkills), projectSkills...) {
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	for _, path := range paths {
		content, found, err := files.ReadText(ctx, path)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		fingerprint = append(fingerprint, path+"\n"+content)
		// pi's rules (`loadSkillFromFile`): a file whose frontmatter does not parse or has no description is skipped (a
		// README next to the skills, say); other spec violations, such as the name's characters or lengths, only warn in
		// pi and load here too. The name defaults to the parent directory's; the first skill of a name wins.
		doc := frontmatter.Parse(content)
		if doc.Err != nil {
			continue
		}
		description, _ := doc.Frontmatter["description"].(string)
		if jsstring.Trim(description) == "" {
			continue
		}
		name, _ := doc.Frontmatter["name"].(string)
		if name == "" {
			name = files.Paths().Basename(files.Paths().Dirname(path))
		}
		if _, taken := skills[name]; !taken {
			disabled, _ := doc.Frontmatter["disable-model-invocation"].(bool)
			skills[name] = &skill{name: name, description: description, path: path, disabled: disabled, global: slices.Contains(globalSkills, path)}
			order = append(order, name)
		}
	}
	// Prompt templates (pi's format): `.md` files directly in the global and the project's prompts directory. An
	// unreadable or invalid template is skipped (pi warns and goes on); symbolic links are not followed.
	var templates []Template
	for _, each := range []struct{ root, source string }{
		{promptsRoot, "global"},
		{files.Paths().Join(cwd, ".pi", "prompts"), "project"},
	} {
		entries, _, err := files.Entries(ctx, each.root)
		if err != nil {
			return nil, err
		}
		entries = slices.DeleteFunc(slices.Clone(entries), func(entry Entry) bool {
			return entry.Kind != "file" || !strings.HasSuffix(entry.Name, ".md")
		})
		slices.SortStableFunc(entries, byName)
		for _, entry := range entries {
			path := files.Paths().Join(each.root, entry.Name)
			content, found, err := files.ReadText(ctx, path)
			if err != nil || !found {
				continue
			}
			fingerprint = append(fingerprint, path+"\n"+content)
			// Invalid frontmatter: skipped.
			template, err := ParseTemplate(entry.Name, content, each.source, path)
			if err == nil && validTemplateName(template.Name) {
				templates = append(templates, template)
			}
		}
	}
	assemble := func(all bool) Resources {
		var kept, visible []*skill
		for _, name := range order {
			if each := skills[name]; all || !each.global {
				kept = append(kept, each)
				if !each.disabled {
					visible = append(visible, each)
				}
			}
		}
		var instructions []string
		for _, entry := range agents {
			if all || !entry.global {
				instructions = append(instructions, entry.text)
			}
		}
		resources := Resources{Agents: strings.Join(instructions, "\n\n"), SkillFiles: []SkillFile{}, Templates: []Template{}}
		if len(visible) > 0 {
			lines := []string{
				"Use the read tool to load a skill's file when the task matches its description.",
				"Resolve relative paths against the skill's directory.",
				"<available_skills>",
			}
			for _, each := range visible {
				lines = append(lines, "<skill><name>"+escapeXML(each.name)+"</name><description>"+escapeXML(each.description)+"</description><location>"+escapeXML(each.path)+"</location></skill>")
			}
			resources.Skills = strings.Join(append(lines, "</available_skills>"), "\n")
		}
		for _, each := range kept {
			resources.SkillFiles = append(resources.SkillFiles, SkillFile{Name: each.name, Description: each.description, Path: each.path, BaseDir: files.Paths().Dirname(each.path)})
		}
		for _, template := range templates {
			if all || template.Source != "global" {
				resources.Templates = append(resources.Templates, template)
			}
		}
		return resources
	}
	project := assemble(false)
	return &LoadedPrompt{Resources: assemble(true), Fingerprint: strings.Join(fingerprint, "\n\x00\n"), Project: &project}, nil
}

// validTemplateName is `/^[^\s/]+$/`.
func validTemplateName(name string) bool {
	return name != "" && !strings.ContainsFunc(name, func(r rune) bool { return r == '/' || jsstring.IsSpace(r) })
}
