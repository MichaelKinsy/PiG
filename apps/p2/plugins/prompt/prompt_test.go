package prompt

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Ports test/p2/context-files.test.ts and the loader, parsing and expansion cases of test/p2/prompt-templates.test.ts (Pi
// main c23b9ec1). Those on the TUI (completion, the skill block's rendering) and the web composer are other lanes.

var ctx = context.Background()

func write(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// remoteFiles is a remote system whose AGENTS.md at one path is "REMOTE PROJECT".
type remoteFiles struct{ agents string }

func (remoteFiles) Paths() PathRules { return LocalPaths }
func (remoteFiles) Local() bool      { return false }
func (f remoteFiles) ReadText(_ context.Context, path string) (string, bool, error) {
	if path == f.agents {
		return "REMOTE PROJECT", true, nil
	}
	return "", false, nil
}
func (remoteFiles) Entries(context.Context, string) ([]Entry, bool, error) { return nil, false, nil }

func TestARemoteFileAtTheLocalGlobalAgentsPathIsItsOwnFile(t *testing.T) {
	directory := t.TempDir()
	write(t, directory, "AGENTS.md", "LOCAL GLOBAL")
	// Local session working in the data directory: the global file once.
	local, err := LoadPromptResources(ctx, directory, directory, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(local.Agents, "LOCAL GLOBAL"); got != 1 {
		t.Errorf("the global file appears %d times: %q", got, local.Agents)
	}
	// Remote session whose cwd has the same path: the remote AGENTS.md there is read as well.
	resources, err := LoadPromptResources(ctx, directory, directory, remoteFiles{agents: filepath.Join(directory, "AGENTS.md")}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resources.Agents, "LOCAL GLOBAL") || !strings.Contains(resources.Agents, "REMOTE PROJECT") {
		t.Errorf("agents = %q", resources.Agents)
	}
}

func TestSkillsFollowPisDiscovery(t *testing.T) {
	directory := t.TempDir()
	skills := filepath.Join(directory, "skills")
	write(t, skills, "release.md", "---\nname: release\ndescription: Root file skill.\n---\nBody")
	write(t, skills, "README.md", "# Not a skill: no frontmatter")
	write(t, skills, "broken.md", "---\nname: [unclosed\n---\n")
	write(t, skills, "deploy/SKILL.md", "---\ndescription: Directory skill, named after its directory.\n---\n")
	write(t, skills, "deploy/notes.md", "---\nname: notes\ndescription: Inside a skill directory: not a skill.\n---\n")
	write(t, skills, "group/nested.md", "---\nname: nested\ndescription: Below the root without SKILL.md: not a skill.\n---\n")
	write(t, skills, "group/odd/SKILL.md", "---\nname: Odd Name\ndescription: pi only warns about the name.\n---\n")
	write(t, skills, "node_modules/dep/SKILL.md", "---\nname: dep\ndescription: Skipped.\n---\n")
	write(t, skills, ".hidden/SKILL.md", "---\nname: hidden\ndescription: Skipped.\n---\n")
	loaded, err := LoadPromptResources(ctx, directory, directory, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var got [][2]string
	for _, skill := range loaded.SkillFiles {
		got = append(got, [2]string{skill.Name, skill.Path})
	}
	want := [][2]string{
		{"deploy", filepath.Join(skills, "deploy", "SKILL.md")},
		{"Odd Name", filepath.Join(skills, "group", "odd", "SKILL.md")},
		{"release", filepath.Join(skills, "release.md")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("skills = %v, want %v", got, want)
	}
}

func TestPisArgumentParsingAndSubstitution(t *testing.T) {
	if got := ParseCommandArgs(`one "two words" 'three'  four`); !reflect.DeepEqual(got, []string{"one", "two words", "three", "four"}) {
		t.Errorf("args = %q", got)
	}
	args := []string{"a", "b", "c"}
	for _, each := range []struct {
		content string
		args    []string
		want    string
	}{
		{"$1-$2-$3-$4", args, "a-b-c-"},
		{"$@|$ARGUMENTS", args, "a b c|a b c"},
		{"${2:-x} ${5:-default}", args, "b default"},
		{"${@:-none}", nil, "none"},
		{"${@:2}|${@:2:1}|${@:0}", args, "b c|b|a b c"},
		// Values are not substituted again.
		{"$1", []string{"$2"}, "$2"},
	} {
		if got := SubstituteArgs(each.content, each.args); got != each.want {
			t.Errorf("SubstituteArgs(%q, %q) = %q, want %q", each.content, each.args, got, each.want)
		}
	}
}

func TestTemplatesPisFileFormatExpansionOfTemplatesAndSkillBlocks(t *testing.T) {
	withHint, err := ParseTemplate("review.md", "---\ndescription: Review a file\nargument-hint: <file>\n---\nReview $1 carefully.", "global", "/p/review.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Template{Name: "review", Description: "Review a file", ArgumentHint: "<file>", Content: "Review $1 carefully.", Source: "global", Path: "/p/review.md"}); withHint != want {
		t.Errorf("template = %+v, want %+v", withHint, want)
	}
	long, err := ParseTemplate("x.md", "\n"+strings.Repeat("long ", 20), "project", "/x.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(long.Description) != 63 {
		t.Errorf("description = %q (%d)", long.Description, len(long.Description))
	}
	skill := SkillFile{Name: "deploy", Description: "Deploys", Path: "/s/deploy/SKILL.md", BaseDir: "/s/deploy"}
	read := func(context.Context, SkillFile) (string, bool, error) {
		return "---\nname: deploy\ndescription: Deploys\n---\nRun the script.\n", true, nil
	}
	expand := func(text string, templates []Template, skills []SkillFile, read ReadSkill) Expanded {
		t.Helper()
		expanded, err := ExpandPrompt(ctx, text, templates, skills, read)
		if err != nil {
			t.Fatal(err)
		}
		return expanded
	}
	if got := expand(`/review "a file.ts"`, []Template{withHint}, []SkillFile{skill}, read); got != (Expanded{Text: "Review a file.ts carefully."}) {
		t.Errorf("template = %+v", got)
	}
	invoked := expand("/skill:deploy now please", []Template{withHint}, []SkillFile{skill}, read)
	if want := "<skill name=\"deploy\" location=\"/s/deploy/SKILL.md\">\nReferences are relative to /s/deploy.\n\nRun the script.\n</skill>\n\nnow please"; invoked.Text != want {
		t.Errorf("skill block = %q, want %q", invoked.Text, want)
	}
	if invoked.Skill == nil || *invoked.Skill != "deploy" {
		t.Errorf("skill = %v", invoked.Skill)
	}
	for _, text := range []string{"/unknown x", "/skill:missing", "plain /review"} {
		if got := expand(text, []Template{withHint}, []SkillFile{skill}, read); got != (Expanded{Text: text}) {
			t.Errorf("%q = %+v", text, got)
		}
	}
	unreadable := func(context.Context, SkillFile) (string, bool, error) { return "", false, nil }
	if got := expand("/skill:deploy", nil, []SkillFile{skill}, unreadable); got != (Expanded{Text: "/skill:deploy"}) {
		t.Errorf("unreadable skill = %+v", got)
	}
}

func TestLoaderGlobalThenProjectTemplatesInvalidOnesSkippedEverySkillInvocable(t *testing.T) {
	directory := t.TempDir()
	cwd := filepath.Join(directory, "project")
	write(t, directory, "prompts/fix.md", "Fix $@")
	write(t, directory, "prompts/broken.md", "---\n: : bad\n---\nx")
	write(t, directory, "prompts/notes.txt", "not a template")
	write(t, cwd, ".pi/prompts/fix.md", "Project fix")
	write(t, cwd, ".pi/prompts/plan.md", "---\ndescription: Plan it\n---\nPlan $1")
	write(t, directory, "skills/hidden/SKILL.md", "---\nname: hidden\ndescription: Only by name\ndisable-model-invocation: true\n---\nSecret steps")
	loaded, err := LoadPromptResources(ctx, directory, cwd, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, template := range loaded.Templates {
		names = append(names, template.Source+":"+template.Name)
	}
	if want := []string{"global:fix", "project:fix", "project:plan"}; !reflect.DeepEqual(names, want) {
		t.Errorf("templates = %v, want %v", names, want)
	}
	expanded, err := ExpandPrompt(ctx, "/fix it", loaded.Templates, nil, nil)
	if err != nil || expanded.Text != "Fix it" {
		t.Errorf("global first: %q, %v", expanded.Text, err)
	}
	if len(loaded.SkillFiles) != 1 || loaded.SkillFiles[0].Name != "hidden" || loaded.SkillFiles[0].BaseDir != filepath.Join(directory, "skills", "hidden") {
		t.Errorf("skill files = %+v", loaded.SkillFiles)
	}
	if loaded.Skills != "" {
		t.Errorf("hidden from the model's list, still invocable: %q", loaded.Skills)
	}
}
