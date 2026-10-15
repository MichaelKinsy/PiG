package prompts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/system-prompt.ts

type promptOracleSkill struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	FilePath               string `json:"filePath"`
	BaseDir                string `json:"baseDir"`
	DisableModelInvocation bool   `json:"disableModelInvocation"`
}

type promptOracleFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type promptOracleProbe struct {
	CustomPrompt       string              `json:"customPrompt,omitempty"`
	SelectedTools      []string            `json:"selectedTools"`
	HiddenTools        []string            `json:"hiddenTools"`
	ToolSnippets       map[string]string   `json:"toolSnippets"`
	ToolGuidelines     map[string][]string `json:"toolGuidelines"`
	PromptGuidelines   []string            `json:"promptGuidelines"`
	AppendSystemPrompt string              `json:"appendSystemPrompt"`
	CustomSections     [][2]string         `json:"customSections"`
	Cwd                string              `json:"cwd"`
	ContextFiles       []promptOracleFile  `json:"contextFiles"`
	Skills             []promptOracleSkill `json:"skills"`
}

// The structured system prompt of buildSystemPromptSections over random tool selections (hidden, with and without snippets and guidelines, bash/PowerShell/grep/find/ls
// rule variants), guidelines that differ by JS whitespace, an appended prompt, custom sections, context files, skills with markup characters and cwd with backslashes.
// PiG differs from Pi in the product name of the preamble and in the docs section (D22, D92), so the default preamble and the docs section are left out;
// every other section must be equal, in order.
func TestSystemPromptSectionsMatchPi(t *testing.T) {
	probes := promptOracleProbes()
	var want []json.RawMessage
	pioracle.Run(t, `
const { buildSystemPromptSections } = await load("pi-coding-agent/core/system-prompt.js");
emit(input.map((probe) => {
	const { customSections, ...rest } = probe;
	const options = { ...rest, customPrompt: probe.customPrompt || undefined, sections: Object.fromEntries(customSections ?? []), skills: (probe.skills ?? []).map((skill) => ({ ...skill, sourceInfo: { path: skill.filePath, source: "local", scope: "user", origin: "top-level" } })) };
	try { return Object.entries(buildSystemPromptSections(options)); } catch (error) { return { error: String(error.message) }; }
}));`, probes, &want)
	failures := 0
	for i, p := range probes {
		got, err := goPromptEntries(p)
		var wantEntries [][2]string
		var wantError struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(want[i], &wantError) == nil && wantError.Error != "" {
			if err == nil || err.Error() != wantError.Error {
				failures++
				t.Errorf("probe %d: PiG error %v, Pi %q", i, err, wantError.Error)
			}
			continue
		}
		if err != nil {
			failures++
			t.Errorf("probe %d: PiG error %v, Pi none", i, err)
			continue
		}
		if err := json.Unmarshal(want[i], &wantEntries); err != nil {
			t.Fatal(err)
		}
		if !comparablePromptEntries(got, wantEntries, p.CustomPrompt != "") {
			if failures++; failures <= 6 {
				t.Errorf("probe %d (%s):\nPiG %s\nPi  %s", i, fmt.Sprintf("tools=%q hidden=%q custom=%q", p.SelectedTools, p.HiddenTools, p.CustomPrompt), render(got), render(wantEntries))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// TestSystemPromptProbesDump prints the corpus for the Pi side of the project-trust/23 parity scenario.
func TestSystemPromptProbesDump(t *testing.T) {
	line, err := json.Marshal(promptOracleProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("system-prompt-probes:%s\n", line)
}

// TestSystemPromptProbesParity prints PiG's sections of each probe (without the product preamble and the docs section, which differ by design: D22, D92) as one JSON line per probe for the project-trust/23 parity scenario.
func TestSystemPromptProbesParity(t *testing.T) {
	for _, p := range promptOracleProbes() {
		entries, err := goPromptEntries(p)
		row := []any{"ok", comparableEntries(entries, p.CustomPrompt != "")}
		if err != nil {
			row = []any{"error", err.Error()}
		}
		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("system-prompt-observation:%s", out.String())
	}
}

// goPromptEntries is PiG's structured prompt of a probe: the built sections with the custom sections applied.
func goPromptEntries(p promptOracleProbe) ([][2]string, error) {
	options := Options{Cwd: p.Cwd, Tools: p.SelectedTools, HiddenTools: p.HiddenTools, ToolHints: p.ToolSnippets, ToolGuidelines: p.ToolGuidelines, PromptGuidelines: p.PromptGuidelines, CustomPrompt: p.CustomPrompt, AppendSystemPrompt: p.AppendSystemPrompt, AppendMode: "replace"}
	for _, f := range p.ContextFiles {
		options.ContextFiles = append(options.ContextFiles, struct{ Path, Content string }{f.Path, f.Content})
	}
	for _, s := range p.Skills {
		options.Skills = append(options.Skills, Skill{Name: s.Name, Description: s.Description, Path: s.FilePath, DisableModelInvocation: s.DisableModelInvocation})
	}
	var custom ai.OrderedSections
	for _, c := range p.CustomSections {
		custom = append(custom, ai.PromptSection{Name: c[0], Value: new(c[1])})
	}
	got, err := ApplyCustomSystemPromptSections(BuildSystemPromptSections(options), custom)
	if err != nil {
		return nil, err
	}
	var entries [][2]string
	for _, section := range got {
		entries = append(entries, [2]string{section.Name, *section.Value})
	}
	return entries, nil
}

// promptOracleProbes is 1500 seeded probes of buildSystemPromptSections' options.
func promptOracleProbes() []promptOracleProbe {
	rng := rand.New(rand.NewSource(227))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	text := func() string {
		return pick("", "alpha", " padded ", "\u00a0nbsp\ufeff", "line1\nline2", "😀", "a <b> & \"c\" 'd'", "\\back\\slash", "tab\there", "\u2028sep")
	}
	names := []string{"read", "bash", "edit", "write", "grep", "find", "ls", "powershell", "custom_a", "custom_b"}
	subset := func(items []string) []string {
		var out []string
		for _, item := range items {
			if rng.Intn(2) == 0 {
				out = append(out, item)
			}
		}
		rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return out
	}
	var probes []promptOracleProbe
	for range 1500 {
		p := promptOracleProbe{ToolSnippets: map[string]string{}, ToolGuidelines: map[string][]string{}, Cwd: pick("/work/dir", `C:\work\dir`, "/a\\b/c", "relative", "/with space")}
		if rng.Intn(3) != 0 {
			p.SelectedTools = subset(names)
		} else {
			p.SelectedTools = []string{"read", "bash", "edit", "write"}
		}
		p.HiddenTools = subset(names)
		for _, name := range names {
			if rng.Intn(2) == 0 {
				p.ToolSnippets[name] = text()
			}
			if rng.Intn(3) == 0 {
				for range 1 + rng.Intn(3) {
					p.ToolGuidelines[name] = append(p.ToolGuidelines[name], pick("Use bash for file operations like ls, rg, find", "Be concise in your responses", " Be concise in your responses\u00a0", text(), "rule "+name))
				}
			}
		}
		for range rng.Intn(4) {
			p.PromptGuidelines = append(p.PromptGuidelines, pick("Show file paths clearly when working with files", "Use bash for file operations like ls, rg, find", text(), "  ", "x"))
		}
		if rng.Intn(3) == 0 {
			p.AppendSystemPrompt = text()
		}
		if rng.Intn(4) == 0 {
			p.CustomPrompt = pick("custom prompt", " custom\n", "😀")
		}
		// A section list is an object in Pi, so a name appears once.
		used := map[string]bool{}
		for range rng.Intn(3) {
			name := pick("extra", "tools", "cwd", "rules", "skills", "my-section_1", "addendum", "project_context")
			if !used[name] {
				used[name] = true
				p.CustomSections = append(p.CustomSections, [2]string{name, text()})
			}
		}
		if rng.Intn(40) == 0 {
			p.CustomSections = append(p.CustomSections, [2]string{pick("Bad", "preamble", "1x", "a b", "é"), "x"})
		}
		for range rng.Intn(3) {
			p.ContextFiles = append(p.ContextFiles, promptOracleFile{Path: pick("/p/AGENTS.md", `C:\p\"q".md`, "/é/x.md"), Content: text()})
		}
		for range rng.Intn(4) {
			skill := promptOracleSkill{Name: pick("one", "two<&>", "x\"y", text()), Description: text(), FilePath: pick("/s/SKILL.md", "/s/<a>&.md", `C:\s\SKILL.md`), DisableModelInvocation: rng.Intn(3) == 0}
			skill.BaseDir = "/s"
			p.Skills = append(p.Skills, skill)
		}
		probes = append(probes, p)
	}
	return probes
}

// comparableEntries drops the product-specific preamble (unless a custom prompt replaced it) and the docs section.
func comparableEntries(entries [][2]string, customPreamble bool) [][2]string {
	out := [][2]string{}
	for _, entry := range entries {
		if entry[0] == "docs" || (entry[0] == "preamble" && !customPreamble) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// comparablePromptEntries compares the sections without the product-specific preamble (unless a custom prompt replaced it) and the docs section.
func comparablePromptEntries(got, want [][2]string, customPreamble bool) bool {
	a, b := comparableEntries(got, customPreamble), comparableEntries(want, customPreamble)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func render(entries [][2]string) string {
	var out []string
	for _, entry := range entries {
		out = append(out, fmt.Sprintf("%s=%q", entry[0], entry[1]))
	}
	return strings.Join(out, " | ")
}
