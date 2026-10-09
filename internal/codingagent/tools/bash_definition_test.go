// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package tools

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/test/tool-system-prompt-contributions.test.ts:26-34 (keeps the bash tool definition aligned with its contribution)
// mutation-checked: dropping the prompt snippet or the guideline from CreateBashToolDefinition fails it
func TestBashToolDefinitionCarriesItsSystemPromptContribution(t *testing.T) {
	definition, err := CreateBashToolDefinition("/workspace", nil)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Name != "bash" || definition.PromptSnippet != "Execute bash commands (ls, grep, find, etc.)" {
		t.Fatalf("name %q, prompt snippet %q", definition.Name, definition.PromptSnippet)
	}
	if want := []string{"You can inspect PI_* environment variables for current model and session details."}; !slices.Equal(definition.PromptGuidelines, want) {
		t.Fatalf("prompt guidelines %v, want %v", definition.PromptGuidelines, want)
	}
	if len(definition.OutputSchema) == 0 {
		t.Fatal("the bash definition declares its structured output schema (bash.ts bashOutputSchema)")
	}
}

// upstream: packages/coding-agent/test/tool-system-prompt-contributions.test.ts:36-43 (keeps bash session-environment guidance conditional)
// mutation-checked: always adding the guideline fails it
func TestBashToolDefinitionDropsTheSessionGuidelineWhenTheEnvironmentIsHidden(t *testing.T) {
	definition, err := CreateBashToolDefinition("/workspace", &BashToolOptions{ExposeSessionEnvironment: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.PromptGuidelines) != 0 {
		t.Fatalf("prompt guidelines %v, want none", definition.PromptGuidelines)
	}
}

// upstream: packages/coding-agent/test/tools.test.ts:1094-1105 (bash uses ctx.cwd when provided)
// mutation-checked: running the command in the construction cwd instead of the invocation context's fails it
func TestBashToolDefinitionRunsInTheInvocationContextCWD(t *testing.T) {
	definition, err := CreateBashToolDefinition(t.TempDir(), &BashToolOptions{ExposeSessionEnvironment: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx := extension.WithContext(t.Context(), extension.NewContext(dir, nil, func() error { return nil }, extension.ContextActions{}))
	args, _ := json.Marshal(map[string]any{"command": "pwd"})
	result, err := definition.Execute(ctx, "test-bash-ctx-cwd", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Text()
	if !strings.Contains(text, dir) {
		t.Fatalf("output %q does not contain the context cwd %q", text, dir)
	}
}
