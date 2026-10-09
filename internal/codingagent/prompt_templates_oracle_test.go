package codingagent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type promptOracleTemplate struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint"`
	Content      string `json:"content"`
	FilePath     string `json:"filePath"`
}

type promptOracleDiagnostic struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    string `json:"path"`
}

type promptOracleLoad struct {
	Templates   []promptOracleTemplate   `json:"templates"`
	Diagnostics []promptOracleDiagnostic `json:"diagnostics"`
	Sources     []PiSourceInfo           `json:"sources"`
}

// promptFixture writes the files of a prompt directory the loader has to tell apart.
func promptFixture(t *testing.T, root string) {
	t.Helper()
	write := func(name, content string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("plain.md", "Just a body\nsecond line\n")
	write("described.md", "---\ndescription: From frontmatter\nargument-hint: <a> [b]\n---\nBody $1\n")
	write("nondescription.md", "---\ndescription: 12\nargument-hint: [1]\n---\n\n\n   first non-empty line   \nmore")
	write("long.md", strings.Repeat("x", 59)+"yz and more text here\n")
	write("exact60.md", strings.Repeat("y", 60)+"\n")
	write("emoji.md", strings.Repeat("😀", 31)+"\n")
	write("bmp.md", strings.Repeat("é", 61)+"\n")
	write("crlf.md", "---\r\ndescription: crlf\r\n---\r\nline1\r\nline2\r\n")
	write("bom.md", "\ufeff---\ndescription: bom\n---\nbody")
	write("empty.md", "")
	write("onlyfence.md", "---\n---\n")
	write("nofence-end.md", "---\ndescription: never closed\nbody")
	write("scalar.md", "---\njust a string\n---\nbody")
	write("list.md", "---\n- a\n- b\n---\nbody")
	write("badyaml.md", "---\ndescription: [unclosed\n---\nbody")
	write("badyaml2.md", "---\na: b: c\n---\nbody")
	write("tabs.md", "---\n\tdescription: tab\n---\nbody")
	write("dup.md", "---\ndescription: a\ndescription: b\n---\nbody")
	write("UPPER.MD", "ignored")
	write("notes.txt", "ignored")
	write(".hidden.md", "hidden body")
	write("two.md.md", "double suffix")
	write("sub/nested.md", "ignored: not recursive")
	write("dirnamed.md/inner.md", "ignored")
	write("white.md", "\u00a0\n\u2003\n\ufeff\n\u0085 spaced\n")
	testenv.Symlink(t, filepath.Join(root, "plain.md"), filepath.Join(root, "link.md"))
	testenv.Symlink(t, filepath.Join(root, "missing.md"), filepath.Join(root, "broken.md"))
	testenv.RequireDirectoryLink(t, filepath.Join(root, "sub"), filepath.Join(root, "dirlink.md"))
}

const promptLoadOracleBody = `
const mod = await load("pi-coding-agent/core/prompt-templates.js");
const out = [];
for (const options of input.loads) {
	const r = mod.loadPromptTemplates(options);
	out.push({
		templates: r.templates.map((t) => ({ name: t.name, description: t.description, argumentHint: t.argumentHint ?? "", content: t.content, filePath: t.filePath })),
		diagnostics: r.diagnostics.map((d) => ({ type: d.type, message: d.message, path: d.path ?? "" })),
		sources: r.templates.map((t) => t.sourceInfo),
	});
}
emit(out);`

// TestLoadPromptTemplatesMatchesPi runs loadPromptTemplates of Pi 1.0.4 and LoadPromptTemplates over one tree of prompt files: file and directory scans, frontmatter forms, description truncation, symlinks, and the source each template reports.
func TestLoadPromptTemplatesMatchesPi(t *testing.T) {
	base := t.TempDir()
	cwd := filepath.Join(base, "cwd")
	agentDir := filepath.Join(base, "agent")
	global := filepath.Join(agentDir, "prompts")
	project := filepath.Join(cwd, ".pi", "prompts")
	explicit := filepath.Join(base, "explicit")
	promptFixture(t, explicit)
	for _, dir := range []string{global, project} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(global, "shared.md"), []byte("global shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "shared.md"), []byte("project shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "only-project.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	lone := filepath.Join(base, "lone.md")
	if err := os.WriteFile(lone, []byte("---\ndescription: lone\n---\nlone body"), 0o644); err != nil {
		t.Fatal(err)
	}
	notMarkdown := filepath.Join(base, "lone.txt")
	if err := os.WriteFile(notMarkdown, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	type loadCase struct {
		Cwd             string   `json:"cwd"`
		AgentDir        string   `json:"agentDir"`
		PromptPaths     []string `json:"promptPaths"`
		IncludeDefaults bool     `json:"includeDefaults"`
	}
	loads := []loadCase{
		{cwd, agentDir, []string{explicit}, false},
		{cwd, agentDir, []string{lone, notMarkdown, filepath.Join(base, "missing"), project}, false},
		{cwd, agentDir, []string{}, true},
		{cwd, agentDir, []string{lone, explicit}, true},
		{cwd, agentDir, []string{global, global, project}, false},
	}
	var want []promptOracleLoad
	pioracle.Run(t, promptLoadOracleBody, map[string]any{"loads": loads}, &want)
	for i, l := range loads {
		got := LoadPromptTemplatesFromOptions(LoadPromptTemplatesOptions(l))
		w := want[i]
		var gotTemplates []promptOracleTemplate
		var gotSources []PiSourceInfo
		for _, tmpl := range got.Templates {
			gotTemplates = append(gotTemplates, promptOracleTemplate{tmpl.Name, tmpl.Description, tmpl.ArgumentHint, tmpl.Content, tmpl.FilePath})
			gotSources = append(gotSources, tmpl.SourceInfo)
		}
		var gotDiagnostics []promptOracleDiagnostic
		for _, d := range got.Diagnostics {
			gotDiagnostics = append(gotDiagnostics, promptOracleDiagnostic{d.Type, d.Message, d.Path})
		}
		if !reflect.DeepEqual(gotTemplates, w.Templates) {
			t.Errorf("load %d templates differ:\n got %+v\nwant %+v", i, gotTemplates, w.Templates)
		}
		// The text of a YAML syntax error is the YAML library's: Pig's library words most classes differently (see the frontmatter survey test); the warning, its path and its place in the order are compared.
		for j := range min(len(gotDiagnostics), len(w.Diagnostics)) {
			if strings.Contains(w.Diagnostics[j].Path, "bad") || strings.HasSuffix(w.Diagnostics[j].Path, "dup.md") || strings.HasSuffix(w.Diagnostics[j].Path, "tabs.md") {
				if filepath.Base(w.Diagnostics[j].Path) == "badyaml2.md" {
					continue
				}
				gotDiagnostics[j].Message = w.Diagnostics[j].Message
			}
		}
		if len(gotDiagnostics) == 0 && len(w.Diagnostics) == 0 {
			gotDiagnostics = w.Diagnostics
		}
		if !reflect.DeepEqual(gotDiagnostics, w.Diagnostics) {
			t.Errorf("load %d diagnostics differ:\n got %+v\nwant %+v", i, gotDiagnostics, w.Diagnostics)
		}
		if !reflect.DeepEqual(gotSources, w.Sources) {
			t.Errorf("load %d sources differ:\n got %+v\nwant %+v", i, gotSources, w.Sources)
		}
	}
}

const promptArgsOracleBody = `
const mod = await load("pi-coding-agent/core/prompt-templates.js");
emit({
	parsed: input.argStrings.map((s) => mod.parseCommandArgs(s)),
	substituted: input.templates.flatMap((t) => input.argLists.map((a) => mod.substituteArgs(t, a))),
	expanded: input.lines.map((line) => mod.expandPromptTemplate(line, input.known.map((n) => ({ name: n, content: "[" + n + " $1|$@|${@:2}]" })))),
});`

// TestPromptArgumentHandlingMatchesPi compares parseCommandArgs, substituteArgs and expandPromptTemplate of Pi 1.0.4 with Pig over argument strings and templates that exercise quoting, JavaScript whitespace, every placeholder form, defaults, slices and non-recursive substitution.
func TestPromptArgumentHandlingMatchesPi(t *testing.T) {
	argStrings := []string{"", "a b c", `"a b" c`, `'a "b"' c`, `a"b c"d e`, `""`, `"" a`, `a\ b`, "a\tb\nc", "a\u00a0b\u2003c\ufeffd\u0085e", `unterminated "quote here`, "   lead  trail   ", `'it''s'`, "é 😀 \"😀 😀\""}
	templates := []string{
		"", "plain", "$1 $2 $3", "$ARGUMENTS|$@|$0|$00|$01|$10|$9", "${1:-d1} ${2:-d2} ${3:-d3}", "${@:-all} ${ARGUMENTS:-all2}", "${@:2} ${@:2:1} ${@:0} ${@:0:2} ${@:3:0} ${@:9}", "${@:1:99}",
		"$ARGUMENTSx $@@ $$1 \\$1 ${1} ${1:-} ${1:-a}b ${1:-}}", "${1:-a}} ${@:2:} ${@:} ${:-x} ${a:-x}", "${1:-$2}", "${@:-$1 $@}", "line1\n$1\nline3 ${2:-nl\nx}", "$1$2$3", "${99999999999999999999:-d}", "$99999999999999999999", "${@:99999999999999999999}", "${1:-é😀}", "${@:2:9223372036854775807}", "${@:1:99999999999999999999}", "${@:3:9223372036854775806}", "${@:9223372036854775807:9223372036854775807}",
	}
	argLists := [][]string{{}, {"a"}, {"a", "b"}, {"a", "b", "c"}, {"", "b"}, {"$1", "$@", "$ARGUMENTS"}, {"x y", "z"}, {"é", "😀"}}
	lines := []string{"", "/", "/known", "/known ", "/known a b", "/known\ta", "/known\u00a0a", "/known\u2003a", "/known\u0085a", "/known\na\nb", "/unknown", " /known", "/known  \"a b\" c", "/ known", "/other x", "/known\ufeffa", "/known\n", "/\nknown"}
	var want struct {
		Parsed      [][]string `json:"parsed"`
		Substituted []string   `json:"substituted"`
		Expanded    []string   `json:"expanded"`
	}
	known := []string{"known", "other"}
	pioracle.Run(t, promptArgsOracleBody, map[string]any{"argStrings": argStrings, "templates": templates, "argLists": argLists, "lines": lines, "known": known}, &want)
	for i, s := range argStrings {
		if got := ParsePromptArgs(s); !reflect.DeepEqual(append([]string{}, got...), append([]string{}, want.Parsed[i]...)) {
			t.Errorf("ParsePromptArgs(%q) = %q, Pi %q", s, got, want.Parsed[i])
		}
	}
	n := 0
	for _, tmpl := range templates {
		for _, args := range argLists {
			if got := SubstitutePromptArgs(tmpl, args); got != want.Substituted[n] {
				t.Errorf("SubstitutePromptArgs(%q, %q) = %q, Pi %q", tmpl, args, got, want.Substituted[n])
			}
			n++
		}
	}
	var promptTemplates []PromptTemplate
	for _, name := range known {
		promptTemplates = append(promptTemplates, PromptTemplate{Name: name, Content: "[" + name + " $1|$@|${@:2}]"})
	}
	for i, line := range lines {
		expanded, ok := ExpandPromptTemplate(line, promptTemplates)
		if !ok {
			expanded = line
		}
		if expanded != want.Expanded[i] {
			t.Errorf("ExpandPromptTemplate(%q) = %q, Pi %q", line, expanded, want.Expanded[i])
		}
	}
}
