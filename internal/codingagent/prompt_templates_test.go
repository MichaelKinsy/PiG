package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// file-based prompt templates.

func TestParsePromptArgs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string{}},
		{"single", "foo", []string{"foo"}},
		{"two", "foo bar", []string{"foo", "bar"}},
		{"tabs", "foo\tbar\tbaz", []string{"foo", "bar", "baz"}},
		{"unicode-whitespace", "foo\nbar\v\fbaz", []string{"foo", "bar", "baz"}},
		{"unicode-words", "voilà Åse café", []string{"voilà", "Åse", "café"}},
		{"multi-space", "foo   bar", []string{"foo", "bar"}},
		{"double-quoted", `foo "hello world" bar`, []string{"foo", "hello world", "bar"}},
		{"single-quoted", `foo 'hello world' bar`, []string{"foo", "hello world", "bar"}},
		{"quote-merging", `pre"in quotes"post`, []string{"prein quotespost"}},
		{"trailing-quote-unterminated", `foo "unterminated`, []string{"foo", "unterminated"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParsePromptArgs(tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got=%#v want=%#v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got=%q want=%q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSubstitutePromptArgs(t *testing.T) {
	cases := []struct {
		name    string
		content string
		args    []string
		want    string
	}{
		{"positional", "first=$1 second=$2", []string{"alpha", "beta"}, "first=alpha second=beta"},
		{"missing-positional", "first=$1 third=$3", []string{"alpha"}, "first=alpha third="},
		{"at-all", "all: $@", []string{"a", "b", "c"}, "all: a b c"},
		{"arguments-alias", "all: $ARGUMENTS", []string{"a", "b"}, "all: a b"},
		{"slice-from", "rest: ${@:2}", []string{"a", "b", "c", "d"}, "rest: b c d"},
		{"slice-range", "two: ${@:2:2}", []string{"a", "b", "c", "d"}, "two: b c"},
		{"slice-zero-treated-as-one", "from-start: ${@:1}", []string{"a", "b"}, "from-start: a b"},
		// Args containing $ patterns must NOT recurse (upstream guarantee).
		{"no-recursion", "x=$1", []string{"$2"}, "x=$2"},
		// Single-pass: a value inserted by one match is not re-scanned by another.
		{"no-recursion-cross-pattern", "$2", []string{"a", "$@"}, "$@"},
		{"mixed", "$1 says $@ to $2", []string{"alice", "bob"}, "alice says alice bob to bob"},
		// ${target:-default}: default used when the arg is missing or empty.
		{"default-present", "v=${1:-fallback}", []string{"given"}, "v=given"},
		{"default-missing", "v=${2:-fallback}", []string{"given"}, "v=fallback"},
		{"default-empty", "v=${1:-fallback}", []string{""}, "v=fallback"},
		{"default-all-args", "v=${@:-none}", []string{"a", "b"}, "v=a b"},
		{"default-all-args-empty", "v=${@:-none}", nil, "v=none"},
		{"default-arguments-alias", "v=${ARGUMENTS:-none}", nil, "v=none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SubstitutePromptArgs(tc.content, tc.args)
			if got != tc.want {
				t.Errorf("got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestLoadPromptTemplates_FromUserAndProject(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	cwd := filepath.Join(dir, "work")
	userPrompts := filepath.Join(agentDir, "prompts")
	projectPrompts := filepath.Join(cwd, ".pig", "prompts")
	if err := os.MkdirAll(userPrompts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectPrompts, 0o755); err != nil {
		t.Fatal(err)
	}

	// User-scoped: simple body, no frontmatter.
	if err := os.WriteFile(filepath.Join(userPrompts, "spec.md"),
		[]byte("Build a spec for $1.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// User-scoped: with frontmatter.
	if err := os.WriteFile(filepath.Join(userPrompts, "review.md"),
		[]byte("---\ndescription: Review code\nargument-hint: <path>\n---\nReview $1\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	// Project-scoped: collides with user `spec`; ordered first-wins keeps user.
	if err := os.WriteFile(filepath.Join(projectPrompts, "spec.md"),
		[]byte("PROJECT spec for $1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Project-scoped: project-only.
	if err := os.WriteFile(filepath.Join(projectPrompts, "deploy.md"),
		[]byte("Deploy $@\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Garbage non-md file should be ignored.
	if err := os.WriteFile(filepath.Join(userPrompts, "ignored.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	templates := LoadPromptTemplates(cwd, agentDir).Templates

	byName := make(map[string]PromptTemplate)
	for _, t := range templates {
		byName[t.Name] = t
	}

	if _, ok := byName["spec"]; !ok {
		t.Fatal("spec missing")
	}
	if !strings.HasPrefix(byName["spec"].Content, "Build a spec") {
		t.Errorf("user should win the ordered collision: got %q", byName["spec"].Content)
	}
	if byName["spec"].Scope != "user" {
		t.Errorf("spec scope=%q want=user", byName["spec"].Scope)
	}
	if byName["review"].Description != "Review code" {
		t.Errorf("review description=%q", byName["review"].Description)
	}
	if byName["review"].ArgumentHint != "<path>" {
		t.Errorf("review argument-hint=%q", byName["review"].ArgumentHint)
	}
	if byName["review"].Scope != "user" {
		t.Errorf("review scope=%q want=user", byName["review"].Scope)
	}
	if _, ok := byName["deploy"]; !ok {
		t.Error("deploy (project-only) missing")
	}
	if _, ok := byName["ignored"]; ok {
		t.Error("non-md file leaked into templates")
	}
}

func TestLoadPromptTemplates_FrontmatterFallbackDescription(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "agent", "prompts")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", 80) + "\nrest of body\n"
	if err := os.WriteFile(filepath.Join(pdir, "long.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpls := LoadPromptTemplates("", filepath.Join(dir, "agent")).Templates
	if len(tmpls) != 1 {
		t.Fatalf("expected 1 template, got %d", len(tmpls))
	}
	if !strings.HasSuffix(tmpls[0].Description, "...") {
		t.Errorf("expected truncated-with-ellipsis description, got %q", tmpls[0].Description)
	}
	if len(tmpls[0].Description) != 63 { // 60 + "..."
		t.Errorf("description length=%d want=63", len(tmpls[0].Description))
	}
}

func TestExpandPromptTemplate(t *testing.T) {
	templates := []PromptTemplate{
		{Name: "spec", Content: "Build $1: $@\n"},
		{Name: "deploy", Content: "Push to $1\n"},
	}

	cases := []struct {
		name      string
		line      string
		wantText  string
		wantMatch bool
	}{
		{"matches-with-args", "/spec auth login flow", "Build auth: auth login flow\n", true},
		{"matches-no-args", "/deploy", "Push to \n", true},
		{"matches-quoted", `/spec "user auth" with mfa`, "Build user auth: user auth with mfa\n", true},
		{"matches-newline-args", "/spec first\nsecond", "Build first: first second\n", true},
		{"matches-leading-vertical-tab", "/spec\vfirst second", "Build first: first second\n", true},
		{"no-match", "/unknown stuff", "", false},
		{"not-slash", "spec something", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ExpandPromptTemplate(tc.line, templates)
			if ok != tc.wantMatch {
				t.Fatalf("matched=%v want=%v", ok, tc.wantMatch)
			}
			if got != tc.wantText {
				t.Errorf("got=%q want=%q", got, tc.wantText)
			}
		})
	}
}

func TestExpandPromptTemplate_BuiltinNotShadowed(t *testing.T) {
	// Defensive: even if a template is named the same as a builtin,
	// ExpandPromptTemplate will match it (it has no knowledge of the
	// registry). The shadowing happens at the dispatch layer in
	// handleSubmit, which checks Resolve() first. This test asserts
	// the template-side behavior; the dispatch-side guarantee is
	// covered by the integration test below.
	templates := []PromptTemplate{{Name: "help", Content: "shadow help\n"}}
	got, ok := ExpandPromptTemplate("/help", templates)
	if !ok || got != "shadow help\n" {
		t.Errorf("template-side expansion failed: ok=%v got=%q", ok, got)
	}
}

// TestLoadPromptTemplates_ExtraDir: a template in an extra dir is found
// and loaded; it can also override a same-named user-scoped template
// ().
func TestLoadPromptTemplates_ExtraDir(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	cwd := filepath.Join(dir, "work")
	userPrompts := filepath.Join(agentDir, "prompts")
	extraDir := filepath.Join(dir, "extra")

	for _, d := range []string{userPrompts, extraDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// User-scoped template.
	if err := os.WriteFile(filepath.Join(userPrompts, "greet.md"),
		[]byte("Hello $1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Extra-dir template: new name.
	if err := os.WriteFile(filepath.Join(extraDir, "extra-cmd.md"),
		[]byte("Extra $1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Extra-dir template: same name loses to the first user-scoped definition.
	if err := os.WriteFile(filepath.Join(extraDir, "greet.md"),
		[]byte("Override Hello $1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	templates := LoadPromptTemplates(cwd, agentDir, extraDir).Templates

	byName := make(map[string]PromptTemplate)
	for _, tmpl := range templates {
		byName[tmpl.Name] = tmpl
	}

	// extra-cmd.md must be found.
	if _, ok := byName["extra-cmd"]; !ok {
		t.Errorf("extra-cmd template not found; got names: %v", templateNames(templates))
	}
	// First-wins keeps the user-scoped greet.
	if g, ok := byName["greet"]; !ok {
		t.Errorf("greet template not found")
	} else if strings.Contains(g.Content, "Override") {
		t.Errorf("later greet unexpectedly won: %q", g.Content)
	}
}

// TestLoadPromptTemplates_EmptyExtraDir: nil/empty extraDirs are
// tolerated without error ().
func TestLoadPromptTemplates_EmptyExtraDir(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(filepath.Join(agentDir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "prompts", "hi.md"),
		[]byte("Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Passing empty strings and a nonexistent dir must not crash.
	templates := LoadPromptTemplates(dir, agentDir, "", filepath.Join(dir, "nonexistent")).Templates
	if len(templates) == 0 {
		t.Errorf("expected at least 1 template; got 0")
	}
}

func TestLoadPromptTemplates_ExtraFilePath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "extra.md")
	if err := os.WriteFile(file, []byte("---\ndescription: extra\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	templates := LoadPromptTemplates("", "", file).Templates
	if len(templates) != 1 {
		t.Fatalf("len = %d, want 1", len(templates))
	}
	if templates[0].Name != "extra" {
		t.Fatalf("name = %q, want extra", templates[0].Name)
	}
}

// templateNames returns the names of a template slice for test error messages.
func templateNames(ts []PromptTemplate) []string {
	names := make([]string, len(ts))
	for i, t := range ts {
		names[i] = t.Name
	}
	return names
}

func TestLoadPromptTemplatesDoesNotImportGlobalPigRootAsProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))
	projectPrompt := filepath.Join(home, ".pig", "prompts", "stale.md")
	if err := os.MkdirAll(filepath.Dir(projectPrompt), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPrompt, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := LoadPromptTemplates(home, filepath.Join(home, ".pig", "agent")).Templates; len(got) != 0 {
		t.Fatalf("global Pig root imported as project prompts: %+v", got)
	}
}

func TestLoadPromptTemplatesRejectsMalformedFrontmatter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.md")
	if err := os.WriteFile(path, []byte("---\ndescription: [unterminated\n---\nBody"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadPromptTemplates("", "", path).Templates; len(got) != 0 {
		t.Fatalf("malformed frontmatter became a command: %#v", got)
	}
}

func TestPromptDescriptionTruncatesAtUnicodeCharacters(t *testing.T) {
	line := strings.Repeat("界", 61)
	path := filepath.Join(t.TempDir(), "unicode.md")
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	result := LoadPromptTemplates("", "", path)
	if len(result.Templates) != 1 {
		t.Fatalf("templates = %#v", result.Templates)
	}
	if got := result.Templates[0].Description; got != strings.Repeat("界", 60)+"..." || !utf8.ValidString(got) {
		t.Fatalf("description = %q", got)
	}
}

func TestInteractivePromptLoadingUsesPreResolvedPathsOnly(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	m.opts.CWD = t.TempDir()
	projectPrompt := filepath.Join(m.opts.CWD, ".pig", "prompts", "project.md")
	if err := os.MkdirAll(filepath.Dir(projectPrompt), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPrompt, []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.PromptPaths = nil
	m.loadPromptTemplates()
	if len(m.promptTemplates) != 0 {
		t.Fatalf("interactive mode rediscovered unapproved project prompts: %#v", m.promptTemplates)
	}
}

func TestPromptDiagnosticsReachInteractiveReload(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	path := filepath.Join(t.TempDir(), "broken.md")
	if err := os.WriteFile(path, []byte("---\ndescription: [unterminated\n---\nBody"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.PromptPaths = []string{path}
	m.loadPromptTemplates()
	if len(m.promptDiagnostics) != 1 || m.promptDiagnostics[0].Type != "warning" || m.promptDiagnostics[0].Path != path {
		t.Fatalf("diagnostics = %#v", m.promptDiagnostics)
	}
	m.showPromptDiagnostics()
	output := stripANSITest(strings.Join(m.chatContainer.Render(300), "\n"))
	if !strings.Contains(output, "[Prompt conflicts]") || !strings.Contains(output, path) || !strings.Contains(output, m.promptDiagnostics[0].Message) {
		t.Fatalf("warning missing: %q", output)
	}
}

func TestPromptFrontmatterUsesYAMLTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "typed.md")
	if err := os.WriteFile(path, []byte("---\ndescription: true\nargument-hint: 42\n---\n  body text  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := LoadPromptTemplates("", "", path)
	if len(result.Diagnostics) != 0 || len(result.Templates) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if got := result.Templates[0]; got.Description != "body text" || got.ArgumentHint != "" || got.Content != "body text" {
		t.Fatalf("template = %#v", got)
	}
}
