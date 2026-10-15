package coding

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type reloadOracleCase struct {
	Files          map[string]string `json:"files"`
	Trusted        bool              `json:"trusted"`
	NoSkills       bool              `json:"noSkills"`
	NoPrompts      bool              `json:"noPrompts"`
	NoThemes       bool              `json:"noThemes"`
	NoContextFiles bool              `json:"noContextFiles"`
	// SystemPrompt and AppendSystemPrompt are the loader options (nil: discover); the Additional lists are the explicit paths. "{root}" is the tree root.
	SystemPrompt       *string  `json:"systemPrompt"`
	AppendSystemPrompt []string `json:"appendSystemPrompt"`
	AdditionalPrompts  []string `json:"additionalPrompts"`
	AdditionalSkills   []string `json:"additionalSkills"`
	AdditionalThemes   []string `json:"additionalThemes"`
}

// TestDefaultResourceLoaderReloadMatchesPi compares resource-loader.ts DefaultResourceLoader.reload with DefaultResourceLoader.Reload over random trees of
// user, project, ancestor, home and settings-listed prompt templates, skills, themes, SYSTEM.md, APPEND_SYSTEM.md and AGENTS.md files, trusted and untrusted,
// with the no-skills, no-prompts, no-themes and no-context-files switches: the resolved prompts, skills and themes (names, descriptions, contents, paths, source
// info), their collision and validation diagnostics, the system prompt and its source, the appended prompts and their sources, and the context files all match.
// Pi installs its theme JSON validator at startup (main.ts), so the oracle does too. The loader options systemPrompt (literal, file, missing, empty), appendSystemPrompt and the three additional-path lists (existing, missing, relative and home-relative) are covered. Not compared: extensions (the Extension Host loads them), Packages (no npm or git source is configured), and the order of theme-less sources beyond what Pi guarantees.
func TestDefaultResourceLoaderReloadMatchesPi(t *testing.T) {
	// go test runs in the package directory; runtime.Caller's file name is a module path under -trimpath, so it cannot locate the mirror.
	darkRaw, err := os.ReadFile(filepath.Join("..", ".upstream", "current", "packages", "coding-agent", "src", "modes", "interactive", "theme", "dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dark map[string]any
	if err := json.Unmarshal(darkRaw, &dark); err != nil {
		t.Fatal(err)
	}
	themeFile := func(name string) string {
		copyTheme := map[string]any{}
		maps.Copy(copyTheme, dark)
		if name == "" {
			delete(copyTheme, "name")
		} else {
			copyTheme["name"] = name
		}
		out, _ := json.Marshal(copyTheme)
		return string(out)
	}

	rng := rand.New(rand.NewSource(20261118))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	promptBodies := []string{
		"---\ndescription: Review the code\nargument-hint: <file>\n---\nReview $1 now",
		"---\ndescription: Only description\n---\nBody text",
		"No frontmatter, first line is the description\nsecond line",
		"", "\n\n  \n", "---\nargument-hint: <x>\n---\n",
		"---\ndescription: 123\nargument-hint: 7\n---\nnumeric fields",
		"---\ndescription: [unclosed\n---\nbad yaml",
		"A very long first line that exceeds sixty characters by a wide margin so the description is truncated",
		"---\ndescription: \"\"\n---\nempty description",
	}
	skillBodies := func(name string) []string {
		return []string{
			"---\nname: " + name + "\ndescription: The " + name + " skill.\n---\n\n# " + name,
			"---\nname: " + name + "\ndescription: Hidden.\ndisable-model-invocation: true\n---\nbody",
			"---\ndescription: No name field\n---\nbody",
			"---\nname: " + name + "\n---\nmissing description",
			"---\nname: Bad_Name\ndescription: invalid characters\n---\nbody",
			"---\nname: other-name\ndescription: name differs from directory\n---\nbody",
			"---\nname: " + name + "\ndescription: " + strings.Repeat("d", 1100) + "\n---\nbody",
			"no frontmatter at all", "",
			"---\nname: " + name + "\ndescription: x\nunknown-key: 1\n---\nbody",
		}
	}
	names := []string{"a", "b", "review", "deploy", "A-b"}
	put := func(files map[string]string, path, content string) { files[path] = content }
	var cases []reloadOracleCase
	for range 400 {
		files := map[string]string{}
		for _, scope := range []string{"agent", "proj/{CFG}"} {
			for range rng.Intn(4) {
				put(files, scope+"/prompts/"+pick(names)+".md", pick(promptBodies))
			}
			for range rng.Intn(3) {
				name := pick(names)
				put(files, scope+"/skills/"+name+"/SKILL.md", pick(skillBodies(name)))
			}
			if rng.Intn(5) == 0 {
				put(files, scope+"/skills/loose.md", pick(skillBodies("loose")))
			}
			for range rng.Intn(3) {
				put(files, scope+"/themes/"+pick(names)+".json", pick([]string{themeFile("t1"), themeFile("t2"), themeFile("t1"), themeFile(""), "{not json", "{}"}))
			}
			for _, sys := range []string{"SYSTEM.md", "APPEND_SYSTEM.md"} {
				if rng.Intn(3) == 0 {
					put(files, scope+"/"+sys, pick([]string{"custom text", "", "line one\nline two\n", "x"}))
				}
			}
		}
		if rng.Intn(2) == 0 {
			put(files, "agent/AGENTS.md", "global agents")
		}
		if rng.Intn(2) == 0 {
			put(files, "proj/AGENTS.md", "project agents")
		}
		if rng.Intn(4) == 0 {
			put(files, "proj/CLAUDE.md", "project claude")
		}
		for range rng.Intn(3) {
			name := pick(names)
			put(files, "proj/.agents/skills/"+name+"/SKILL.md", pick(skillBodies(name)))
		}
		if rng.Intn(3) == 0 {
			name := pick(names)
			put(files, "home/.agents/skills/"+name+"/SKILL.md", pick(skillBodies(name)))
		}
		for range rng.Intn(3) {
			put(files, "extra/prompts/"+pick(names)+".md", pick(promptBodies))
		}
		for range rng.Intn(2) {
			name := pick(names)
			put(files, "extra/skills/"+name+"/SKILL.md", pick(skillBodies(name)))
		}
		put(files, "extra/themes/"+pick(names)+".json", themeFile("t3"))
		settings := func() string {
			var members []string
			if rng.Intn(2) == 0 {
				members = append(members, `"prompts":["{root}/extra/prompts"]`)
			}
			if rng.Intn(2) == 0 {
				members = append(members, `"skills":["{root}/extra/skills","-skills/a"]`)
			}
			if rng.Intn(3) == 0 {
				members = append(members, `"themes":["{root}/extra/themes"]`)
			}
			if len(members) == 0 {
				return ""
			}
			return "{" + strings.Join(members, ",") + "}"
		}
		if s := settings(); s != "" {
			put(files, "agent/settings.json", s)
		}
		if s := settings(); s != "" {
			put(files, "proj/{CFG}/settings.json", s)
		}
		c := reloadOracleCase{Files: files, Trusted: rng.Intn(3) != 0, NoSkills: rng.Intn(6) == 0, NoPrompts: rng.Intn(6) == 0, NoThemes: rng.Intn(6) == 0, NoContextFiles: rng.Intn(6) == 0}
		if rng.Intn(3) == 0 {
			c.SystemPrompt = new(pick([]string{"literal system text", "{root}/extra/sys.md", "{root}/missing-sys.md", "", "{root}/agent/SYSTEM.md"}))
		}
		if rng.Intn(3) == 0 {
			c.AppendSystemPrompt = []string{}
			for range rng.Intn(4) {
				c.AppendSystemPrompt = append(c.AppendSystemPrompt, pick([]string{"literal append", "{root}/extra/app.md", "{root}/missing-app.md", "", "{root}/extra/empty.md"}))
			}
		}
		put(files, "extra/sys.md", "\ufeffsystem from file")
		put(files, "extra/app.md", "append from file\n")
		put(files, "extra/empty.md", "")
		for range rng.Intn(3) {
			c.AdditionalPrompts = append(c.AdditionalPrompts, pick([]string{"{root}/extra/prompts", "{root}/extra/prompts/a.md", "{root}/missing-prompts", "{root}/proj/{CFG}/prompts", "extra/prompts", "~/prompts"}))
		}
		for range rng.Intn(3) {
			c.AdditionalSkills = append(c.AdditionalSkills, pick([]string{"{root}/extra/skills", "{root}/extra/skills/a", "{root}/missing-skills", "{root}/agent/skills", "extra/skills"}))
		}
		for range rng.Intn(3) {
			c.AdditionalThemes = append(c.AdditionalThemes, pick([]string{"{root}/extra/themes", "{root}/missing-themes", "{root}/agent/themes", "extra/themes/a.json"}))
		}
		cases = append(cases, c)
	}

	var want []map[string]any
	pioracle.Run(t, `
const { DefaultResourceLoader } = await load("pi-coding-agent/core/resource-loader.js");
const { SettingsManager } = await load("pi-coding-agent/core/settings-manager.js");
const { CONFIG_DIR_NAME } = await load("pi-coding-agent/config.js");
const { setThemeJsonValidator } = await load("pi-coding-agent/modes/interactive/theme/theme.js");
const { validateThemeJson } = await load("pi-coding-agent/modes/interactive/theme/theme-schema.js");
setThemeJsonValidator(validateThemeJson);
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const c of input) {
	// realpathSync keeps a Windows 8.3 short name ("RUNNER~1") as given. A path inside a JSON file carries the root JSON-escaped, and so
	// does the JSON of a result, which is where the root is replaced.
	const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "reloadr-")));
	const escapedRoot = JSON.stringify(root).slice(1, -1);
	const sub = (s) => s.split("{CFG}").join(CONFIG_DIR_NAME).split("{root}").join(root);
	const subContent = (rel, s) => rel.endsWith(".json") ? s.split("{CFG}").join(CONFIG_DIR_NAME).split("{root}").join(escapedRoot) : sub(s);
	fs.mkdirSync(path.join(root, "home"), { recursive: true });
	fs.mkdirSync(path.join(root, "proj"), { recursive: true });
	fs.mkdirSync(path.join(root, "agent"), { recursive: true });
	for (const [rel, content] of Object.entries(c.files)) {
		const file = path.join(root, sub(rel));
		fs.mkdirSync(path.dirname(file), { recursive: true });
		fs.writeFileSync(file, subContent(rel, content));
	}
	const cwd = path.join(root, "proj"), agentDir = path.join(root, "agent");
	process.env.HOME = path.join(root, "home");
	const settingsManager = SettingsManager.create(cwd, agentDir, { projectTrusted: c.trusted });
	const loader = new DefaultResourceLoader({ cwd, agentDir, settingsManager, noExtensions: true,
		noSkills: c.noSkills, noPromptTemplates: c.noPrompts, noThemes: c.noThemes, noContextFiles: c.noContextFiles,
		systemPrompt: c.systemPrompt === null ? undefined : sub(c.systemPrompt),
		appendSystemPrompt: c.appendSystemPrompt === null ? undefined : c.appendSystemPrompt.map(sub),
		additionalPromptTemplatePaths: (c.additionalPrompts ?? []).map(sub), additionalSkillPaths: (c.additionalSkills ?? []).map(sub), additionalThemePaths: (c.additionalThemes ?? []).map(sub) });
	let row;
	try {
		await loader.reload();
		const skills = loader.getSkills(), prompts = loader.getPrompts(), themes = loader.getThemes();
		row = {
			skills: skills.skills.map((s) => ({ name: s.name, description: s.description, filePath: s.filePath, baseDir: s.baseDir, disableModelInvocation: !!s.disableModelInvocation, sourceInfo: s.sourceInfo })),
			skillDiagnostics: skills.diagnostics,
			prompts: prompts.prompts.map((p) => ({ name: p.name, description: p.description, argumentHint: p.argumentHint ?? null, content: p.content, filePath: p.filePath, sourceInfo: p.sourceInfo })),
			promptDiagnostics: prompts.diagnostics,
			themes: themes.themes.map((t) => ({ name: t.name ?? null, sourcePath: t.sourcePath ?? null, sourceInfo: t.sourceInfo ?? null })),
			themeDiagnostics: themes.diagnostics,
			systemPrompt: loader.getSystemPrompt() ?? null,
			systemPromptSource: loader.getSystemPromptSource() ?? null,
			append: loader.getAppendSystemPrompt(),
			appendSources: loader.getAppendSystemPromptSources(),
			agentsFiles: loader.getAgentsFiles().agentsFiles,
		};
	} catch (e) { row = { error: e.message }; }
	out.push(JSON.parse(JSON.stringify(row).split(escapedRoot).join("{root}")));
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);`, cases, &want)

	failures := 0
	for i, c := range cases {
		root := t.TempDir()
		// Node's realpathSync, which the oracle uses, keeps a Windows 8.3 short name that EvalSymlinks expands.
		if resolved, err := filepath.EvalSymlinks(root); err == nil && runtime.GOOS != "windows" {
			root = resolved
		}
		escapedRoot := jsonEscaped(root)
		cfg := filepath.Base(icodingagent.ProjectConfigDir(filepath.Join(root, "proj")))
		sub := func(s string) string { return strings.NewReplacer("{CFG}", cfg, "{root}", root).Replace(s) }
		subContent := func(rel, s string) string {
			if strings.HasSuffix(rel, ".json") {
				return strings.NewReplacer("{CFG}", cfg, "{root}", escapedRoot).Replace(s)
			}
			return sub(s)
		}
		for _, dir := range []string{"home", "proj", "agent"} {
			_ = os.MkdirAll(filepath.Join(root, dir), 0o755)
		}
		for rel, content := range c.Files {
			file := filepath.Join(root, sub(rel))
			_ = os.MkdirAll(filepath.Dir(file), 0o755)
			if err := os.WriteFile(file, []byte(subContent(rel, content)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("HOME", filepath.Join(root, "home"))
		cwd, agentDir := filepath.Join(root, "proj"), filepath.Join(root, "agent")
		loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir,
			SettingsManager: icodingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, c.Trusted), NoExtensions: true,
			NoSkills: c.NoSkills, NoPromptTemplates: c.NoPrompts, NoThemes: c.NoThemes, NoContextFiles: c.NoContextFiles,
			SystemPrompt: substituted(c.SystemPrompt, sub), AppendSystemPrompt: substitutedList(c.AppendSystemPrompt, sub),
			AdditionalPromptTemplatePaths: substitutedList(c.AdditionalPrompts, sub), AdditionalSkillPaths: substitutedList(c.AdditionalSkills, sub),
			AdditionalThemePaths: substitutedList(c.AdditionalThemes, sub)})
		var got map[string]any
		if err := loader.Reload(); err != nil {
			got = map[string]any{"error": err.Error()}
		} else {
			got = reloadSnapshot(t, loader)
		}
		encoded, _ := json.Marshal(got)
		var normalized map[string]any
		_ = json.Unmarshal([]byte(strings.ReplaceAll(string(encoded), escapedRoot, "{root}")), &normalized)
		if !reflect.DeepEqual(normalized, want[i]) {
			failures++
			if failures <= 5 {
				var diffs []string
				for key, value := range normalized {
					if !reflect.DeepEqual(value, want[i][key]) {
						goJSON, _ := json.Marshal(value)
						piJSON, _ := json.Marshal(want[i][key])
						diffs = append(diffs, fmt.Sprintf("%s\n Go %.600s\n Pi %.600s", key, goJSON, piJSON))
					}
				}
				files, _ := json.Marshal(c)
				t.Errorf("case %d %.1500s:\n%s", i, files, strings.Join(diffs, "\n"))
			}
		}
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}

func reloadSnapshot(t *testing.T, loader *DefaultResourceLoader) map[string]any {
	info := func(v any) any {
		data, _ := json.Marshal(v)
		var out any
		_ = json.Unmarshal(data, &out)
		return out
	}
	skills, prompts, themes := loader.GetSkills(), loader.GetPrompts(), loader.GetThemes()
	row := map[string]any{"skillDiagnostics": info(orEmpty(skills.Diagnostics)), "promptDiagnostics": info(orEmpty(prompts.Diagnostics)), "themeDiagnostics": info(orEmpty(themes.Diagnostics))}
	skillRows := []any{}
	for _, s := range skills.Skills {
		skillRows = append(skillRows, map[string]any{"name": s.Name, "description": s.Description, "filePath": s.FilePath, "baseDir": s.BaseDir, "disableModelInvocation": s.DisableModelInvocation, "sourceInfo": info(s.SourceInfo)})
	}
	promptRows := []any{}
	for _, p := range prompts.Prompts {
		var hint any
		if p.ArgumentHint != "" {
			hint = p.ArgumentHint
		}
		promptRows = append(promptRows, map[string]any{"name": p.Name, "description": p.Description, "argumentHint": hint, "content": p.Content, "filePath": p.FilePath, "sourceInfo": info(p.SourceInfo)})
	}
	themeRows := []any{}
	for _, th := range themes.Themes {
		var sourcePath any
		if th.SourcePath != "" {
			sourcePath = th.SourcePath
		}
		themeRows = append(themeRows, map[string]any{"name": th.Name, "sourcePath": sourcePath, "sourceInfo": info(th.SourceInfo)})
	}
	row["skills"], row["prompts"], row["themes"] = skillRows, promptRows, themeRows
	row["systemPrompt"], row["systemPromptSource"] = nil, nil
	if text, ok := loader.GetSystemPrompt(); ok {
		row["systemPrompt"] = text
	}
	if source, ok := loader.GetSystemPromptSource(); ok {
		row["systemPromptSource"] = map[string]any{"path": source.Path}
	}
	row["append"] = info(orEmpty(loader.GetAppendSystemPrompt()))
	appendSources := []any{}
	for _, source := range loader.GetAppendSystemPromptSources() {
		appendSources = append(appendSources, map[string]any{"path": source.Path})
	}
	row["appendSources"] = appendSources
	agentsFiles := []any{}
	for _, file := range loader.GetAgentsFiles().AgentsFiles {
		agentsFiles = append(agentsFiles, map[string]any{"path": file.Path, "content": file.Content})
	}
	row["agentsFiles"] = agentsFiles
	return row
}

// jsonEscaped is text as it appears inside a JSON string, so a path with backslashes can be found in encoded JSON.
func jsonEscaped(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded[1 : len(encoded)-1])
}

func substituted(value *string, sub func(string) string) *string {
	if value == nil {
		return nil
	}
	return new(sub(*value))
}

func substitutedList(values []string, sub func(string) string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = sub(v)
	}
	return out
}

func orEmpty[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
