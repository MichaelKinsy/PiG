package inproc

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func promptOptionsFixture() extension.BuildSystemPromptOptions {
	options := extension.BuildSystemPromptOptions{
		Cwd:                "/work/project",
		SelectedTools:      []string{"read", "bash", "edit", "write"},
		ToolSnippets:       map[string]string{"read": "Read files", "bash": "Run commands", "edit": "Edit files", "write": "Write files"},
		PromptGuidelines:   []string{"Be concise.", "Show file paths clearly."},
		AppendSystemPrompt: "Appended.",
	}
	for i := range 4 {
		options.ContextFiles = append(options.ContextFiles, extension.SystemPromptContextFile{Path: fmt.Sprintf("/work/AGENTS-%d.md", i), Content: "Rules of the project, line one.\nLine two.\n"})
		options.Skills = append(options.Skills, extension.SystemPromptSkill{Name: fmt.Sprintf("skill-%d", i), Description: "A skill", FilePath: fmt.Sprintf("/skills/%d/SKILL.md", i)})
	}
	return options
}

func beforeAgentStartRunner(t testing.TB, handlers int, handler extension.HandlerFn) *Runner {
	t.Helper()
	exts := make([]extension.Extension, handlers)
	for i := range exts {
		exts[i] = extension.Extension{Path: fmt.Sprintf("/ext/%d", i), Handlers: map[string][]extension.HandlerFn{"before_agent_start": {handler}}}
	}
	runner := NewRunner(exts, t.TempDir())
	t.Cleanup(func() { runner.Invalidate("test complete") })
	return runner
}

// A handler that leaves the options alone costs the dispatch no prompt render of its own: the options every later handler sees are the ones the last render built from (runner.ts:1426 renderCurrentSystemPrompt is a pure function of them).
func TestBeforeAgentStartRendersThePromptOnlyWhenTheOptionsChange(t *testing.T) {
	options := promptOptionsFixture()
	noop := func(args ...any) (any, error) { return nil, nil }
	allocs := func(handlers int) float64 {
		runner := beforeAgentStartRunner(t, handlers, noop)
		return testing.AllocsPerRun(20, func() {
			if _, err := runner.EmitBeforeAgentStart(context.Background(), extension.UserPromptTrigger("prompt", nil), options); err != nil {
				t.Fatal(err)
			}
		})
	}
	one, ten := allocs(1), allocs(10)
	perHandler := (ten - one) / 9
	t.Logf("allocs: 1 handler %.0f, 10 handlers %.0f, per extra handler %.1f", one, ten, perHandler)
	const bound = 40
	if perHandler > bound {
		t.Fatalf("each extra no-op handler allocates %.1f times; the prompt render is repeated per handler (bound %d)", perHandler, bound)
	}
}

// Every kind of edit a handler can make to the shared options reaches the prompt the next handler reads, through its event and through ctx.getSystemPrompt(), and an unedited run still reads the base prompt (runner.ts:1426).
func TestBeforeAgentStartPromptFollowsEveryOptionsEdit(t *testing.T) {
	edits := map[string]func(*extension.BuildSystemPromptOptions){
		"append a selected tool":        func(o *extension.BuildSystemPromptOptions) { o.SelectedTools = append(o.SelectedTools, "grep") },
		"edit a selected tool in place": func(o *extension.BuildSystemPromptOptions) { o.SelectedTools[0] = "ls" },
		"edit a context file in place":  func(o *extension.BuildSystemPromptOptions) { o.ContextFiles[0].Content = "Edited rules." },
		"edit a skill in place":         func(o *extension.BuildSystemPromptOptions) { o.Skills[0].Description = "Edited skill" },
		"edit a guideline in place":     func(o *extension.BuildSystemPromptOptions) { o.PromptGuidelines[0] = "Edited guideline." },
		"edit a tool snippet in place":  func(o *extension.BuildSystemPromptOptions) { o.ToolSnippets["read"] = "Edited snippet" },
		"change the append text":        func(o *extension.BuildSystemPromptOptions) { o.AppendSystemPrompt = "Edited append." },
		"add a section": func(o *extension.BuildSystemPromptOptions) {
			*o.Sections = append(*o.Sections, ai.PromptSection{Name: "extra", Value: new("Extra text.")})
		},
		"force the prompt": func(o *extension.BuildSystemPromptOptions) { o.ForceSystemPrompt = new("Forced.") },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			base := promptOptionsFixture()
			if len(base.ContextFiles) == 0 {
				t.Fatal("fixture has no context files")
			}
			var seen []string
			var getter []string
			first := true
			handler := func(args ...any) (any, error) {
				prompt, err := args[0].(extension.BeforeAgentStartEvent).SystemPrompt.Get()
				if err != nil {
					t.Error(err)
				}
				seen = append(seen, prompt)
				ctx := args[1].(context.Context)
				text, err := extension.FromContext(ctx).GetSystemPrompt()
				if err != nil {
					t.Error(err)
				}
				getter = append(getter, text)
				if first {
					first = false
					edit(extension.BeforeAgentStartOptions(ctx))
				}
				return nil, nil
			}
			runner := beforeAgentStartRunner(t, 3, handler)
			if _, err := runner.EmitBeforeAgentStart(context.Background(), extension.UserPromptTrigger("prompt", nil), base); err != nil {
				t.Fatal(err)
			}
			edited := extension.NormalizeBuildSystemPromptOptions(base)
			edit(&edited)
			want, err := RenderSystemPrompt(edited)
			if err != nil {
				t.Fatal(err)
			}
			unedited, err := RenderSystemPrompt(extension.NormalizeBuildSystemPromptOptions(base))
			if err != nil {
				t.Fatal(err)
			}
			if want == unedited {
				t.Fatal("the edit does not change the rendered prompt; the case proves nothing")
			}
			if len(seen) != 3 || seen[0] != unedited || seen[1] != want || seen[2] != want {
				t.Errorf("event prompts = %q, want [unedited, edited, edited]", seen)
			}
			if len(getter) != 3 || getter[0] != unedited || getter[1] != want || getter[2] != want {
				t.Errorf("getSystemPrompt = %q, want [unedited, edited, edited]", getter)
			}
		})
	}
}
