//go:build !pig_strip_node_extensions

package codingagent_test

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestToolRendererExamplesUpstream(t *testing.T) {
	// .upstream/v0.99.2/packages/coding-agent/test/tool-renderer-examples.test.ts:65
	// Regression test for https://github.com/earendil-works/pi/issues/10072
	for _, tc := range []struct {
		name, file string
		tools      []string
	}{
		{"built-in tool renderer", "built-in-tool-renderer.ts", []string{"read", "bash", "edit", "write"}},
		{"minimal mode", "minimal-mode.ts", []string{"read", "bash", "write", "edit", "find", "grep", "ls"}},
	} {
		t.Run("keeps the system prompt unchanged for the "+tc.name+" example", func(t *testing.T) {
			tempDir := t.TempDir()
			baseline, _, _ := rendererExampleState(t, tempDir, "", tc.tools)
			withRenderer, _, _ := rendererExampleState(t, tempDir, filepath.Join(examplesDir, tc.file), tc.tools)
			if withRenderer != baseline {
				t.Fatalf("system prompt changed by %s:\n--- baseline\n%s\n--- with renderer\n%s", tc.file, baseline, withRenderer)
			}
		})
	}
	// .upstream/v0.99.2/packages/coding-agent/test/tool-renderer-examples.test.ts:86
	// Regression test for https://github.com/earendil-works/pi/issues/10072
	t.Run("keeps minimal mode's edit tool in the default shell", func(t *testing.T) {
		_, editToolDefinition, ok := rendererExampleState(t, t.TempDir(), filepath.Join(examplesDir, "minimal-mode.ts"), []string{"edit"})
		if !ok {
			t.Fatal("minimal mode did not register the edit tool")
		}
		defaultShellDefinition := editToolDefinition
		defaultShellDefinition.RenderShell = ""

		got, want := renderEditTool(t, editToolDefinition), renderEditTool(t, defaultShellDefinition)
		if !slices.Equal(got, want) {
			t.Fatalf("edit card = %q, want the default shell %q", got, want)
		}
	})
}
