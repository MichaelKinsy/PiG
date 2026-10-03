package durableagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// promptFixture is a project directory with an AGENTS.md and a skill, an agent directory with a global AGENTS.md, and the settings that point at the skill.
type promptFixture struct {
	cwd, skillPath string
	settings       *codingagent.SettingsManager
}

func newPromptFixture(t *testing.T) promptFixture {
	t.Helper()
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_USE_PI_DIRS", "")
	cwd := filepath.Join(root, "project")
	skillPath := filepath.Join(root, "skills", "release", "SKILL.md")
	write(t, filepath.Join(agentDir, "AGENTS.md"), "global rules")
	write(t, filepath.Join(cwd, "AGENTS.md"), "project rules")
	write(t, skillPath, "---\nname: release\ndescription: Cut a release\n---\nSteps.\n")
	write(t, filepath.Join(agentDir, "settings.json"), `{"skills":[`+quote(filepath.Dir(skillPath))+`]}`)
	return promptFixture{cwd: cwd, skillPath: skillPath, settings: codingagent.NewSettingsManager(cwd, agentDir)}
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }

func renderAll(t *testing.T, extension *durable.Extension, input durable.PromptInput) map[string]string {
	t.Helper()
	rendered := map[string]string{}
	for _, section := range extension.Sections {
		if section.Tag == nil || *section.Tag {
			t.Fatalf("section %s is tagged; the built sections carry their own tags", section.Key)
		}
		text, err := section.Render(context.Background(), input)
		if err != nil {
			t.Fatalf("section %s: %v", section.Key, err)
		}
		if text != nil {
			rendered[section.Key] = *text
		}
	}
	return rendered
}

func toolRegistrations(t *testing.T, names ...string) []*durable.ToolRegistration {
	t.Helper()
	var registrations []*durable.ToolRegistration
	for _, name := range names {
		registrations = append(registrations, &durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: name}})
	}
	return registrations
}

// prompt.ts:20-74: the extension is "pi-prompt" with the seven sections in pi's order, untagged, built from the request's tools and the conversation's directory.
func TestCreatePiPromptSections(t *testing.T) {
	fixture := newPromptFixture(t)
	extension := CreatePiPrompt(fixture.settings, fixture.cwd)
	if extension.Name != "pi-prompt" {
		t.Fatalf("name = %q", extension.Name)
	}
	var keys []string
	for _, section := range extension.Sections {
		keys = append(keys, section.Key)
	}
	if got, want := strings.Join(keys, ","), "preamble,tools,rules,docs,project_context,skills,cwd"; got != want {
		t.Fatalf("sections = %s, want %s", got, want)
	}

	rendered := renderAll(t, extension, durable.PromptInput{Agent: durable.Agent{Tools: toolRegistrations(t, "read", "bash", "edit", "write", "subagent")}})
	// PiG identity: the preamble names pig as the harness and never says pi.
	if got, want := rendered["preamble"], "You are an expert coding assistant operating inside pig, a coding agent harness. You help users by reading files, executing commands, editing code, and writing new files."; got != want {
		t.Fatalf("preamble = %q", got)
	}
	// Only the four coding tools contribute a snippet; any other selected tool is named without one and so is left out of the list.
	wantTools := "<tools>\n- read: Read file contents\n- bash: Execute bash commands (ls, grep, find, etc.)\n- edit: Make precise file edits with exact text replacement, including multiple disjoint edits in one call\n- write: Create or overwrite files\n\nIn addition to the tools above, you may have access to other custom tools depending on the project.\n</tools>"
	if rendered["tools"] != wantTools {
		t.Fatalf("tools = %q\nwant %q", rendered["tools"], wantTools)
	}
	for _, guideline := range []string{
		"Use read to examine files instead of cat or sed.",
		"You can inspect PI_* environment variables for current model and session details.",
		"Use edit for precise changes (edits[].oldText must match exactly)",
		"Use write only for new files or complete rewrites.",
	} {
		if !strings.Contains(rendered["rules"], guideline) {
			t.Errorf("rules lack %q:\n%s", guideline, rendered["rules"])
		}
	}
	project := rendered["project_context"]
	for _, want := range []string{"global rules", "project rules", `path="` + filepath.Join(fixture.cwd, "AGENTS.md") + `"`} {
		if !strings.Contains(project, want) {
			t.Errorf("project_context lacks %q:\n%s", want, project)
		}
	}
	if !strings.Contains(rendered["skills"], "<name>release</name>") || !strings.Contains(rendered["skills"], fixture.skillPath) {
		t.Errorf("skills = %q", rendered["skills"])
	}
	if got := rendered["cwd"]; got != "<cwd>\n"+filepath.ToSlash(fixture.cwd)+"\n</cwd>" {
		t.Fatalf("cwd = %q, want the fallback directory %q", got, fixture.cwd)
	}
}

// prompt.ts:41-44: the directory is the environment's, then the agent's, then the fallback.
func TestCreatePiPromptDirectoryPrecedence(t *testing.T) {
	fixture := newPromptFixture(t)
	extension := CreatePiPrompt(fixture.settings, fixture.cwd)
	agentCwd, envCwd := t.TempDir(), t.TempDir()
	input := durable.PromptInput{Agent: durable.Agent{Cwd: &agentCwd, Tools: toolRegistrations(t, "read")}}
	if got := renderAll(t, extension, input)["cwd"]; got != "<cwd>\n"+filepath.ToSlash(agentCwd)+"\n</cwd>" {
		t.Fatalf("agent cwd: %q, want %q", got, agentCwd)
	}
	input.Env = mustEnv(t, NewExecutionEnvs(envCwd), harness.EnvTarget{})
	if got := renderAll(t, extension, input)["cwd"]; got != "<cwd>\n"+filepath.ToSlash(envCwd)+"\n</cwd>" {
		t.Fatalf("environment cwd: %q, want %q", got, envCwd)
	}
}

// prompt.ts:27-37: context files and skills load once per directory, like pi at startup.
func TestCreatePiPromptLoadsResourcesOncePerDirectory(t *testing.T) {
	fixture := newPromptFixture(t)
	extension := CreatePiPrompt(fixture.settings, fixture.cwd)
	input := durable.PromptInput{Agent: durable.Agent{Tools: toolRegistrations(t, "read")}}
	if !strings.Contains(renderAll(t, extension, input)["project_context"], "project rules") {
		t.Fatal("the first render loads the project rules")
	}
	write(t, filepath.Join(fixture.cwd, "AGENTS.md"), "changed after startup")
	if got := renderAll(t, extension, input)["project_context"]; !strings.Contains(got, "project rules") || strings.Contains(got, "changed after startup") {
		t.Fatalf("the loaded directory is cached, got %q", got)
	}
	// Another directory loads for itself.
	other := t.TempDir()
	write(t, filepath.Join(other, "AGENTS.md"), "other rules")
	input.Agent.Cwd = &other
	if got := renderAll(t, extension, input)["project_context"]; !strings.Contains(got, "other rules") || strings.Contains(got, "project rules") {
		t.Fatalf("a second directory loads its own context, got %q", got)
	}
}

// The coding registry feeds the Harness: a request's system prompt is the tagged sections of pi's prompt for the conversation's tools and directory.
func TestCodingRegistrySystemPromptReachesTheProvider(t *testing.T) {
	fixture := newPromptFixture(t)
	registry, err := CreateCodingRegistry(fixture.settings, fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	var systemPrompt string
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(transcript ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
		systemPrompt = ai.GetCurrentSystemPrompt(transcript.Messages())
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}}, nil
	})})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	opened, err := harness.OpenHarness(context.Background(), storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry, Settings: CreateHarnessSettings(fixture.settings)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(context.Background()) })
	model := faux.GetModel()
	root, err := opened.Root(context.Background(), &harness.RootOptions{Agent: &harness.AgentChange{
		Cwd:   harness.SetTo(fixture.cwd),
		Model: harness.SetTo(durable.ModelRef{Provider: model.ProviderMeta.ProviderID, ModelId: model.ID}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := root.Submit(context.Background(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := submission.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(systemPrompt, "You are an expert coding assistant operating inside pig, a coding agent harness.") {
		t.Fatalf("system prompt does not start with the pig preamble:\n%s", systemPrompt)
	}
	for _, want := range []string{"<tools>\n- read: Read file contents\n- write: Create or overwrite files\n- edit:", "- bash: Execute bash commands", "</tools>", "<rules>", "<project_context>", "project rules", "<skills>", "<cwd>\n" + filepath.ToSlash(fixture.cwd) + "\n</cwd>"} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("system prompt lacks %q:\n%s", want, systemPrompt)
		}
	}
}
