package evalsuites

// Ports packages/evals/evals/prompt-templates.docs.eval.ts.
//
// Pi's output reads session.resourceLoader. A PiG Session runs in a pig process, so the output loads the prompt
// templates and skills the way a Session's resource loader does, from the run's workspace and agent directory (a
// DefaultResourceLoader without extensions), and reads the loaded extensions from the process (AgentRun.Extensions).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/evals"
)

const (
	templateName   = "current-time"
	templateCmd    = "/" + templateName
	templatePrompt = "Tell me the current time and timezone."
)

type promptTemplateScenario struct{ title, request, scope string }

var promptTemplateScenarios = []promptTemplateScenario{
	{
		title:   "Create a project prompt template",
		request: fmt.Sprintf("This project is already trusted. Add a %s prompt template for this project containing exactly: %s", templateCmd, templatePrompt),
		scope:   "project",
	},
	{
		title:   "Create a user prompt template",
		request: fmt.Sprintf("Make a %s prompt template available in all my PiG sessions containing exactly: %s", templateCmd, templatePrompt),
		scope:   "user",
	},
}

// lastUserText is the text of the last user message of the Session, trimmed; "" when there is none.
func lastUserText(messages []map[string]any) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index]["role"] != "user" {
			continue
		}
		switch content := messages[index]["content"].(type) {
		case string:
			return strings.TrimSpace(content)
		case []any:
			var texts []string
			for _, block := range content {
				if record, ok := block.(map[string]any); ok && record["type"] == "text" {
					text, _ := record["text"].(string)
					texts = append(texts, text)
				}
			}
			return strings.TrimSpace(strings.Join(texts, "\n"))
		}
		return ""
	}
	return ""
}

// promptTemplateOutput is the output of the prompt template evals for the scope the template must have.
func promptTemplateOutput(_ context.Context, run evals.AgentRun, scope string) (any, error) {
	projectPath := filepath.Join(run.Workspace, icodingagent.ConfigDirName(), "prompts", templateName+".md")
	userPath := filepath.Join(run.AgentDir, "prompts", templateName+".md")
	expectedPath, otherPath := projectPath, userPath
	if scope == "user" {
		expectedPath, otherPath = userPath, projectPath
	}
	loader := coding.NewDefaultResourceLoader(coding.DefaultResourceLoaderOptions{CWD: run.Workspace, AgentDir: run.AgentDir, NoExtensions: true})
	defer loader.Close()
	if err := loader.Reload(); err != nil {
		return nil, err
	}
	prompts, skills := loader.GetPrompts(), loader.GetSkills()
	var matching []coding.PromptTemplate
	for _, prompt := range prompts.Prompts {
		if prompt.Name == templateName {
			matching = append(matching, prompt)
		}
	}
	var template any
	if len(matching) == 1 {
		found := matching[0]
		template = map[string]any{"scope": found.SourceInfo.Scope, "atExpectedPath": samePath(found.FilePath, expectedPath)}
	}
	diagnostics := []string{}
	for _, diagnostic := range prompts.Diagnostics {
		diagnostics = append(diagnostics, diagnostic.Message)
	}
	skillNames := []string{}
	for _, skill := range skills.Skills {
		skillNames = append(skillNames, skill.Name)
	}
	_, otherErr := os.Stat(otherPath)
	return map[string]any{
		"template":                 template,
		"promptDiagnostics":        diagnostics,
		"extensions":               append([]string{}, run.Extensions...),
		"extensionErrors":          extensionErrors(run.ExtensionErrors),
		"skills":                   skillNames,
		"otherScopeTemplateExists": otherErr == nil,
		"expandedToTimeRequest":    lastUserText(run.Messages) == templatePrompt,
	}, nil
}

// samePath compares two paths after resolving symbolic links, as the workspace may sit behind one.
func samePath(left, right string) bool {
	resolve := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return filepath.Clean(path)
	}
	return resolve(left) == resolve(right)
}

// PromptTemplates asks the agent to create a prompt template, reload, and invoke it, once per scope.
func PromptTemplates() []evals.Suite {
	var suites []evals.Suite
	for _, scenario := range promptTemplateScenarios {
		suites = append(suites, evals.Suite{
			Name: scenario.title,
			File: "evals/prompt-templates.docs.eval.go",
			Harness: documentationHarness(evals.PiCodingAgentHarnessOptions{Output: func(ctx context.Context, run evals.AgentRun) (any, error) {
				return promptTemplateOutput(ctx, run, scenario.scope)
			}}),
			Judges: []evals.Judge{mustJudge(map[string]any{
				"template":                 map[string]any{"scope": scenario.scope, "atExpectedPath": true},
				"promptDiagnostics":        []any{},
				"extensions":               []any{},
				"extensionErrors":          []any{},
				"skills":                   []any{},
				"otherScopeTemplateExists": false,
				"expandedToTimeRequest":    true,
			})},
			NoJudgeThreshold: true,
			Cases: []evals.Case{singleCase(fmt.Sprintf("creates, reloads, and invokes %s", templateCmd), evals.PiCodingAgentInput{
				{Type: evals.PiCodingAgentStepPrompt, Content: scenario.request},
				{Type: evals.PiCodingAgentStepReload},
				{Type: evals.PiCodingAgentStepPrompt, Content: templateCmd},
			})},
		})
	}
	return suites
}
