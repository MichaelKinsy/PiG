package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// introspectionFixture is a user config root with one ambient skill and a project that holds an extension, a skill, a prompt, a
// theme and settings, as the self-inspection review built it.
type introspectionFixture struct {
	root, agentDir, cwd string
}

func newIntrospectionFixture(t *testing.T) introspectionFixture {
	t.Helper()
	root := t.TempDir()
	f := introspectionFixture{root: root, agentDir: filepath.Join(root, "agent"), cwd: filepath.Join(root, "workspace")}
	t.Setenv("PIG_HOME", root)
	t.Setenv("PIG_CODING_AGENT_DIR", f.agentDir)
	t.Setenv("HOME", root)
	t.Setenv(piglet.ActiveSourceEnv, "")
	t.Chdir(mustMkdirAll(t, f.cwd))
	writeResourceFixture(t, filepath.Join(f.agentDir, "skills", "gskill", "SKILL.md"), "---\nname: gskill\ndescription: user skill\n---\nbody\n")
	project := filepath.Join(f.cwd, ".pig")
	writeResourceFixture(t, filepath.Join(project, "extensions", "proj.js"), "export default function () {}\n")
	writeResourceFixture(t, filepath.Join(project, "skills", "myskill", "SKILL.md"), "---\nname: myskill\ndescription: project skill\n---\nbody\n")
	writeResourceFixture(t, filepath.Join(project, "prompts", "pp.md"), "project prompt\n")
	writeThemeFixture(t, filepath.Join(project, "themes", "pt.json"), func(map[string]any) {})
	return f
}

func mustMkdirAll(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (f introspectionFixture) setTrust(t *testing.T, trusted bool) {
	t.Helper()
	if err := codingagent.NewProjectTrustStore(f.agentDir).Set(f.cwd, &trusted); err != nil {
		t.Fatal(err)
	}
}

func runStatusJSON(t *testing.T) statusOutput {
	t.Helper()
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--json", "--no-input"}) })
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var status statusOutput
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func statusItem(t *testing.T, status statusOutput, kind, name string) statusResourceItem {
	t.Helper()
	for _, item := range status.Resources.Items {
		if item.Kind == kind && item.Name == name {
			return item
		}
	}
	t.Fatalf("no %s %q in %#v", kind, name, status.Resources.Items)
	return statusResourceItem{}
}

// Bug 1: status listed an untrusted project's resources as enabled with no health problem, while the run that held the same
// trust decision loaded none of them.
func TestStatusMarksUntrustedProjectResourcesIgnored(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, introspectionFixture)
	}{
		{"no saved decision", func(*testing.T, introspectionFixture) {}},
		{"saved untrusted", func(t *testing.T, f introspectionFixture) { f.setTrust(t, false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIntrospectionFixture(t)
			tc.setup(t, f)
			status := runStatusJSON(t)
			if status.Trust.State != projectTrustUntrusted || status.Trust.Reason == "" {
				t.Fatalf("trust = %#v", status.Trust)
			}
			for _, want := range []struct{ kind, name string }{{"extensions", "proj"}, {"skills", "myskill"}, {"prompts", "pp"}, {"themes", "pt"}} {
				item := statusItem(t, status, want.kind, want.name)
				if item.Ignored != statusIgnoredUntrusted || item.Enabled || item.Scope != "project" {
					t.Errorf("%s %s = %#v, want ignored %q and not enabled", want.kind, want.name, item, statusIgnoredUntrusted)
				}
			}
			if gskill := statusItem(t, status, "skills", "gskill"); gskill.Ignored != "" || !gskill.Enabled {
				t.Errorf("user skill = %#v, want it to apply", gskill)
			}
			if status.Resources.Ignored != 4 || status.Resources.Enabled != 1 || status.Resources.Total != 5 {
				t.Errorf("counts = %#v", status.Resources)
			}
		})
	}
}

func TestStatusTrustedProjectResourcesApply(t *testing.T) {
	f := newIntrospectionFixture(t)
	f.setTrust(t, true)
	status := runStatusJSON(t)
	if status.Trust.State != projectTrustTrusted {
		t.Fatalf("trust = %#v", status.Trust)
	}
	for _, want := range []struct{ kind, name string }{{"extensions", "proj"}, {"skills", "myskill"}, {"prompts", "pp"}, {"themes", "pt"}} {
		if item := statusItem(t, status, want.kind, want.name); item.Ignored != "" || !item.Enabled {
			t.Errorf("%s %s = %#v, want it to apply", want.kind, want.name, item)
		}
	}
	if status.Resources.Ignored != 0 {
		t.Errorf("ignored = %d", status.Resources.Ignored)
	}
}

func TestStatusHumanOutputNamesTrustAndIgnoredResources(t *testing.T) {
	newIntrospectionFixture(t)
	stdout, _, code := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status"}) })
	if code != 0 || !strings.Contains(stdout, "project trust: untrusted") || !strings.Contains(stdout, "ignored (untrusted): skills myskill") {
		t.Fatalf("code=%d stdout=%s", code, stdout)
	}
}

func TestReportProjectTrustNamesTheDeciding(t *testing.T) {
	for _, tc := range []struct {
		name       string
		project    bool
		saved      *bool
		global     string
		wantState  string
		wantReason string
	}{
		{"nothing to trust", false, nil, "", projectTrustNotRequired, "no settings or resources"},
		{"saved trusted", true, new(true), "never", projectTrustTrusted, "saved decision"},
		{"saved untrusted beats always", true, new(false), "always", projectTrustUntrusted, "saved decision"},
		{"default always", true, nil, "always", projectTrustTrusted, "defaultProjectTrust is always"},
		{"default never", true, nil, "never", projectTrustUntrusted, "defaultProjectTrust is never"},
		{"ask without a prompt", true, nil, "", projectTrustUntrusted, "treat the project as untrusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "workspace")
			t.Setenv("HOME", root)
			mustMkdirAll(t, cwd)
			mustMkdirAll(t, agentDir)
			if tc.project {
				writeResourceFixture(t, filepath.Join(cwd, ".pig", "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\n")
			}
			if tc.saved != nil {
				if err := codingagent.NewProjectTrustStore(agentDir).Set(cwd, tc.saved); err != nil {
					t.Fatal(err)
				}
			}
			if tc.global != "" {
				writeResourceFixture(t, filepath.Join(agentDir, "settings.json"), `{"defaultProjectTrust":"`+tc.global+`"}`)
			}
			got := reportProjectTrust(cwd, agentDir)
			if got.State != tc.wantState || !strings.Contains(got.Reason, tc.wantReason) {
				t.Fatalf("report = %#v, want %s / %q", got, tc.wantState, tc.wantReason)
			}
		})
	}
}

// Bug 4: status ignored the active Piglet's discovery, so it listed ambient resources the session never saw.
func TestStatusAppliesTheActivePigletDiscovery(t *testing.T) {
	f := newIntrospectionFixture(t)
	f.setTrust(t, true)
	pigletFile := filepath.Join(f.root, "reviewer.yaml")
	writeResourceFixture(t, pigletFile, "name: reviewer\ndiscovery:\n  skills: [user]\n  extensions: []\n")
	t.Setenv(piglet.ActiveSourceEnv, pigletFile)
	status := runStatusJSON(t)
	if status.ActivePiglet == nil || status.ActivePiglet.Name != "reviewer" || status.ActivePiglet.Source != pigletFile || status.ActivePiglet.Binary {
		t.Fatalf("activePiglet = %#v", status.ActivePiglet)
	}
	if item := statusItem(t, status, "skills", "gskill"); item.Ignored != "" || !item.Enabled {
		t.Errorf("user skill = %#v, want it to apply: the Piglet lists user", item)
	}
	if item := statusItem(t, status, "skills", "myskill"); item.Ignored != statusIgnoredPiglet || item.Enabled {
		t.Errorf("project skill = %#v, want ignored by the Piglet's discovery", item)
	}
	if item := statusItem(t, status, "extensions", "proj"); item.Ignored != statusIgnoredPiglet || item.Enabled {
		t.Errorf("project extension = %#v, want ignored by the Piglet's discovery", item)
	}
	if item := statusItem(t, status, "prompts", "pp"); item.Ignored != "" || !item.Enabled {
		t.Errorf("prompt = %#v: a Piglet's discovery does not cover prompts", item)
	}
}

// Only a Piglet Binary knows whether it accepts overlays, so another pig names the Binary and says it cannot apply its discovery
// instead of guessing which resources it excluded.
func TestStatusNamesAPigletBinaryItCannotApply(t *testing.T) {
	f := newIntrospectionFixture(t)
	f.setTrust(t, true)
	t.Setenv(piglet.ActiveSourceEnv, "tern")
	status := runStatusJSON(t)
	if status.ActivePiglet == nil || status.ActivePiglet.Name != "tern" || !status.ActivePiglet.Binary {
		t.Fatalf("activePiglet = %#v", status.ActivePiglet)
	}
	if len(status.Warnings) != 1 || !strings.Contains(status.Warnings[0], `"tern" is a Piglet Binary`) {
		t.Fatalf("warnings = %v", status.Warnings)
	}
	for _, want := range []struct{ kind, name string }{{"extensions", "proj"}, {"skills", "myskill"}, {"skills", "gskill"}} {
		if item := statusItem(t, status, want.kind, want.name); item.Ignored != "" {
			t.Errorf("%s %s = %#v: no discovery was applied", want.kind, want.name, item)
		}
	}
}

func TestStatusWarnsWhenTheActivePigletSourceDoesNotLoad(t *testing.T) {
	f := newIntrospectionFixture(t)
	f.setTrust(t, true)
	t.Setenv(piglet.ActiveSourceEnv, filepath.Join(f.root, "missing.yaml"))
	status := runStatusJSON(t)
	if len(status.Warnings) != 1 || !strings.Contains(status.Warnings[0], piglet.ActiveSourceEnv) {
		t.Fatalf("warnings = %v", status.Warnings)
	}
	if item := statusItem(t, status, "skills", "gskill"); item.Ignored != "" {
		t.Errorf("an unloadable source must not exclude resources: %#v", item)
	}
}

// Bug 4: the session exports its Piglet to the processes its tools start.
func TestExportActivePigletSourceReachesToolProcesses(t *testing.T) {
	toolEnv := func() string {
		for _, kv := range tools.GetShellEnv("") {
			if value, ok := strings.CutPrefix(kv, piglet.ActiveSourceEnv+"="); ok {
				return value
			}
		}
		return ""
	}
	file := &piglet.Piglet{Name: "reviewer"}
	exportActivePigletSource(file, false)
	if got := toolEnv(); got != "reviewer" {
		t.Fatalf("a Piglet without a source file exports its name, got %q", got)
	}
	exportActivePigletSource(nil, false)
	if got := toolEnv(); got != "" {
		t.Fatalf("no active Piglet must clear a value inherited from a parent session, got %q", got)
	}
	exportActivePigletSource(file, true)
	if got := toolEnv(); got != "reviewer" {
		t.Fatalf("a Piglet Binary exports its name, got %q", got)
	}
}

const benchModelsJSON = `{"providers":{"bench":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"synthetic-test-key","models":[` +
	`{"id":"bench-1","name":"Bench 1","reasoning":false,"input":["text"],"contextWindow":128000,"maxTokens":1024,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}},` +
	`{"id":"bench-2","name":"Bench 2","reasoning":false,"input":["text"],"contextWindow":128000,"maxTokens":1024,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`

func diagnoseOutput(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	runDiagnose(&buf, "/test/binary")
	return buf.String()
}

func defaultModelSection(t *testing.T, out string) string {
	t.Helper()
	_, section, ok := strings.Cut(out, "\ndefault model\n")
	if !ok {
		t.Fatalf("no default model section in:\n%s", out)
	}
	section, _, _ = strings.Cut(section, "\nenvironment\n")
	return section
}

func newBenchFixture(t *testing.T, globalSettings, projectSettings string) introspectionFixture {
	t.Helper()
	f := newIntrospectionFixture(t)
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GROQ_API_KEY", "CEREBRAS_API_KEY"} {
		t.Setenv(key, "")
	}
	writeResourceFixture(t, filepath.Join(f.agentDir, "models.json"), benchModelsJSON)
	writeResourceFixture(t, filepath.Join(f.agentDir, "settings.json"), globalSettings)
	writeResourceFixture(t, filepath.Join(f.cwd, ".pig", "settings.json"), projectSettings)
	return f
}

// Bug 2: diagnose reported an untrusted project's defaultModel, which the session did not use.
func TestDiagnoseIgnoresUntrustedProjectDefaultModel(t *testing.T) {
	f := newBenchFixture(t, `{"defaultProvider":"bench","defaultModel":"bench-1"}`, `{"defaultProvider":"bench","defaultModel":"bench-2"}`)
	section := defaultModelSection(t, diagnoseOutput(t))
	projectFile := filepath.Join(f.cwd, ".pig", "settings.json")
	for _, want := range []string{
		"project trust:  untrusted",
		"configured:     bench/bench-1 (defaultProvider from " + filepath.Join(f.agentDir, "settings.json") + ", defaultModel from " + filepath.Join(f.agentDir, "settings.json") + ")",
		"effective:      bench/bench-1\n",
		"ignored:        " + projectFile + " sets defaultProvider \"bench\" and defaultModel \"bench-2\"",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("missing %q in:\n%s", want, section)
		}
	}
	if strings.Contains(section, "bench/bench-2 (") {
		t.Errorf("an untrusted project's model is reported as the default:\n%s", section)
	}
}

func TestDiagnoseReportsTrustedProjectDefaultModelAndItsFile(t *testing.T) {
	f := newBenchFixture(t, `{"defaultProvider":"bench","defaultModel":"bench-1"}`, `{"defaultModel":"bench-2"}`)
	f.setTrust(t, true)
	out := diagnoseOutput(t)
	section := defaultModelSection(t, out)
	projectFile := filepath.Join(f.cwd, ".pig", "settings.json")
	for _, want := range []string{
		"project trust:  trusted",
		"configured:     bench/bench-2 (defaultProvider from " + filepath.Join(f.agentDir, "settings.json") + ", defaultModel from " + projectFile + ")",
		"effective:      bench/bench-2\n",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("missing %q in:\n%s", want, section)
		}
	}
	if !strings.Contains(out, "\nproject trust:     trusted (saved decision for this folder)\n") {
		t.Errorf("the paths section has no trust line:\n%s", out)
	}
}

// Bug 3: Pi reads defaultModel as a model id (settings-manager.ts getDefaultModel, model-resolver.ts findInitialModel calls
// modelRegistry.find(defaultProvider, defaultModelId)), so "bench/bench-2" names no model and startup falls back to the first
// available one. Diagnose printed it as "bench/bench/bench-2" and implied that model was in use.
func TestDiagnoseQualifiedDefaultModelFallsBackAsPiDoes(t *testing.T) {
	newBenchFixture(t, `{"defaultProvider":"bench","defaultModel":"bench/bench-2"}`, `{}`)
	section := defaultModelSection(t, diagnoseOutput(t))
	if strings.Contains(section, "bench/bench/bench-2 (from settings)") {
		t.Errorf("the old doubled line is back:\n%s", section)
	}
	for _, want := range []string{
		"effective:      bench/bench-1 (startup falls back to it)",
		"bench/bench/bench-2 is not an available model.",
		`set defaultProvider "bench" and defaultModel "bench-2"`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("missing %q in:\n%s", want, section)
		}
	}
}

func TestDefaultModelResolutionDoesNotSplitAProviderPrefix(t *testing.T) {
	f := newBenchFixture(t, `{"defaultProvider":"bench","defaultModel":"bench/bench-2"}`, `{}`)
	services := testServices(t, f.agentDir)
	defer services.Close()
	runtime := services.ModelRuntime()
	selected, err := findInitialModel(newLazyStartupModelRuntime(runtime.RuntimeModels, runtime.HasConfiguredAuth), "bench", "bench/bench-2")
	if err != nil || selected == nil || selected.ID != "bench-1" {
		t.Fatalf("selected = %#v, err = %v: Pi does not split a provider prefix, so startup falls back to the first model", selected, err)
	}
}

func TestDiagnoseDefaultModelWithoutProviderIsIgnoredLikePi(t *testing.T) {
	newBenchFixture(t, `{"defaultModel":"bench-2"}`, `{}`)
	section := defaultModelSection(t, diagnoseOutput(t))
	for _, want := range []string{"defaultModel \"bench-2\" from", "which startup ignores", "effective:      bench/bench-1 (startup falls back to it)"} {
		if !strings.Contains(section, want) {
			t.Errorf("missing %q in:\n%s", want, section)
		}
	}
}

// Bug 4: diagnose sees the active Piglet of the session that started it and the model that Piglet sets.
func TestDiagnoseNamesTheActivePiglet(t *testing.T) {
	f := newBenchFixture(t, `{"defaultProvider":"bench","defaultModel":"bench-1"}`, `{}`)
	pigletFile := filepath.Join(f.root, "reviewer.yaml")
	writeResourceFixture(t, pigletFile, "name: reviewer\nmodel:\n  provider: bench\n  name: bench-2\n")
	t.Setenv(piglet.ActiveSourceEnv, pigletFile)
	out := diagnoseOutput(t)
	if !strings.Contains(out, "\nactive Piglet:     reviewer ("+pigletFile+")") {
		t.Errorf("no active Piglet line in:\n%s", out)
	}
	if section := defaultModelSection(t, out); !strings.Contains(section, "active Piglet:  reviewer sets model bench/bench-2") {
		t.Errorf("no Piglet model line in:\n%s", section)
	}
	t.Setenv(piglet.ActiveSourceEnv, "")
	if out := diagnoseOutput(t); !strings.Contains(out, "\nactive Piglet:     none\n") {
		t.Errorf("no 'none' line in:\n%s", out)
	}
}

func writeThemeFixture(t *testing.T, path string, mutate func(colors map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]any
	if err := json.Unmarshal(data, &theme); err != nil {
		t.Fatal(err)
	}
	mutate(theme["colors"].(map[string]any))
	data, err = json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}
	writeResourceFixture(t, path, string(data))
}

// Pi's loader leaves out a skill without a description (skills.ts loadSkillFromFile), so a session that has the file does not
// have the skill. Status listed it enabled with no health problem.
func TestStatusReportsASkillWithoutDescriptionAsNotLoaded(t *testing.T) {
	f := newIntrospectionFixture(t)
	f.setTrust(t, true)
	writeResourceFixture(t, filepath.Join(f.agentDir, "skills", "nodesc", "SKILL.md"), "---\nname: nodesc\n---\nbody\n")
	writeResourceFixture(t, filepath.Join(f.agentDir, "skills", "blank", "SKILL.md"), "---\nname: blank\ndescription: \"  \"\n---\nbody\n")
	status := runStatusJSON(t)
	for _, name := range []string{"nodesc", "blank"} {
		item := statusItem(t, status, "skills", name)
		if item.Health != "not loaded: description is required" {
			t.Errorf("%s health = %q", name, item.Health)
		}
	}
	if item := statusItem(t, status, "skills", "gskill"); item.Health != "" {
		t.Errorf("a skill with a description reports health %q", item.Health)
	}
	if !status.Healthy || len(status.Errors) != 0 {
		t.Errorf("Pi only warns about it: healthy=%v errors=%v", status.Healthy, status.Errors)
	}
	for _, want := range []string{"skills nodesc: not loaded: description is required", "skills blank: not loaded: description is required"} {
		if !slices.Contains(status.Warnings, want) {
			t.Errorf("warnings = %v, missing %q", status.Warnings, want)
		}
	}
}

// Pi validates a theme against its schema, which names every color role, so a theme with an unknown role does not load.
func TestStatusReportsAThemeWithAnUnknownColorRoleAsInvalid(t *testing.T) {
	f := newIntrospectionFixture(t)
	f.setTrust(t, true)
	writeThemeFixture(t, filepath.Join(f.agentDir, "themes", "good.json"), func(map[string]any) {})
	writeThemeFixture(t, filepath.Join(f.agentDir, "themes", "bad.json"), func(colors map[string]any) { colors["notARole"] = "#ffffff" })
	status := runStatusJSON(t)
	if item := statusItem(t, status, "themes", "good"); item.Health != "" {
		t.Errorf("a valid theme reports health %q", item.Health)
	}
	bad := statusItem(t, status, "themes", "bad")
	if !strings.HasPrefix(bad.Health, "invalid: ") || !strings.Contains(bad.Health, "notARole") {
		t.Errorf("bad theme health = %q", bad.Health)
	}
	if !status.Healthy {
		t.Errorf("Pi only warns about it: errors=%v", status.Errors)
	}
}
