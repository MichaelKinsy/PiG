package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Ports packages/coding-agent/test/resource-loader.test.ts:499-577 through the prompt resolution path shared by startup and reload. The additional empty cases mirror resolvePromptInput and appendSources at resource-loader.ts:54-69,532-545.
func TestUpstreamResourceLoaderPromptSources(t *testing.T) {
	for _, tc := range []struct {
		name, custom, appendText string
		files                    map[string]string
		flags                    Args
		sources                  []string
		// customSet mirrors Pi's defined customPrompt: resolvePromptInput returns undefined only for an empty source (resource-loader.ts:54-57,526-528).
		customSet bool
	}{
		{name: "project-system", files: map[string]string{"project/.pig/SYSTEM.md": "Project system prompt."}, custom: "Project system prompt.", sources: []string{"project/.pig/SYSTEM.md"}, customSet: true},
		{name: "global-system", files: map[string]string{"agent/SYSTEM.md": "Global system prompt."}, custom: "Global system prompt.", sources: []string{"agent/SYSTEM.md"}, customSet: true},
		{name: "literal-system", flags: Args{SystemPrompt: "Literal system prompt."}, custom: "Literal system prompt.", customSet: true},
		{name: "file-system", files: map[string]string{"custom-system.md": "Custom system prompt."}, flags: Args{SystemPrompt: "@custom-system.md"}, custom: "Custom system prompt.", sources: []string{"custom-system.md"}, customSet: true},
		{name: "project-append", files: map[string]string{"project/.pig/APPEND_SYSTEM.md": "Project append prompt."}, appendText: "Project append prompt.", sources: []string{"project/.pig/APPEND_SYSTEM.md"}},
		{name: "literal-append", flags: Args{AppendSystemPrompt: []string{"Literal append prompt."}}, appendText: "Literal append prompt."},
		{name: "file-and-literal-append", files: map[string]string{"custom-append.md": "Custom append prompt."}, flags: Args{AppendSystemPrompt: []string{"@custom-append.md", "Literal append prompt."}}, appendText: "Custom append prompt.\n\nLiteral append prompt.", sources: []string{"custom-append.md"}},
		{name: "empty-file-system", files: map[string]string{"agent/SYSTEM.md": ""}, customSet: true, sources: []string{"agent/SYSTEM.md"}},
		{name: "explicit-empty-system", files: map[string]string{"agent/SYSTEM.md": "Do not discover."}, flags: parseArgs([]string{"--system-prompt", ""})},
		{name: "explicit-empty-append", files: map[string]string{"agent/APPEND_SYSTEM.md": "Do not discover."}, flags: Args{AppendSystemPrompt: []string{}}},
		{name: "empty-file-append", files: map[string]string{"empty.md": ""}, flags: Args{AppendSystemPrompt: []string{"@empty.md", "Literal append prompt."}}, appendText: "\n\nLiteral append prompt.", sources: []string{"empty.md"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PIG_HOME", filepath.Join(root, "config"))
			for path, content := range tc.files {
				path = filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			resolve := func(value string) string {
				if len(value) > 0 && value[0] == '@' {
					return filepath.Join(root, value[1:])
				}
				return value
			}
			flags := tc.flags
			flags.SystemPrompt = resolve(flags.SystemPrompt)
			for i, value := range flags.AppendSystemPrompt {
				flags.AppendSystemPrompt[i] = resolve(value)
			}
			want := resolvedPromptInputs{custom: tc.custom, customSet: tc.customSet, append: tc.appendText}
			for _, path := range tc.sources {
				want.sourcePaths = append(want.sourcePaths, filepath.Join(root, path))
			}
			got := resolvePromptInputs(filepath.Join(root, "project"), filepath.Join(root, "agent"), flags, true)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("resolved = %#v, want %#v", got, want)
			}
		})
	}
}
