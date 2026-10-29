package evalsuites

// Ports the output function of packages/evals/evals/prompt-templates.docs.eval.ts over the files a run leaves behind.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/evals"
)

func writeTemplate(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "current-time.md"), []byte(templatePrompt+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func templateRun(t *testing.T, messages ...map[string]any) evals.AgentRun {
	t.Helper()
	return evals.AgentRun{AgentDir: filepath.Join(t.TempDir(), "agent"), Workspace: t.TempDir(), Messages: messages}
}

func TestPromptTemplateOutputReportsTheScopeAndTheExpandedPrompt(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			run := templateRun(t,
				map[string]any{"role": "user", "content": "Add the template"},
				map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "done"}}},
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": " " + templatePrompt + "\n"}}},
			)
			if scope == "project" {
				writeTemplate(t, filepath.Join(run.Workspace, icodingagent.ConfigDirName(), "prompts"))
			} else {
				writeTemplate(t, filepath.Join(run.AgentDir, "prompts"))
			}
			got, err := promptTemplateOutput(t.Context(), run, scope)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{
				"template":                 map[string]any{"scope": scope, "atExpectedPath": true},
				"promptDiagnostics":        []string{},
				"extensions":               []string{},
				"extensionErrors":          []string{},
				"skills":                   []string{},
				"otherScopeTemplateExists": false,
				"expandedToTimeRequest":    true,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("output = %#v, want %#v", got, want)
			}
		})
	}
}

func TestPromptTemplateOutputFlagsATemplateInTheWrongScopeAndAnUnexpandedCommand(t *testing.T) {
	run := templateRun(t, map[string]any{"role": "user", "content": "/current-time"})
	writeTemplate(t, filepath.Join(run.AgentDir, "prompts"))
	writeTemplate(t, filepath.Join(run.Workspace, icodingagent.ConfigDirName(), "prompts"))
	got, err := promptTemplateOutput(t.Context(), run, "project")
	if err != nil {
		t.Fatal(err)
	}
	output := got.(map[string]any)
	if output["otherScopeTemplateExists"] != true || output["expandedToTimeRequest"] != false {
		t.Fatalf("output = %#v", output)
	}
}

func TestPromptTemplateOutputHasNoTemplateWhenNothingWasWritten(t *testing.T) {
	got, err := promptTemplateOutput(t.Context(), templateRun(t), "user")
	if err != nil {
		t.Fatal(err)
	}
	if output := got.(map[string]any); output["template"] != nil || output["expandedToTimeRequest"] != false {
		t.Fatalf("output = %#v", output)
	}
}
