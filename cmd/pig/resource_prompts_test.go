package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports packages/coding-agent/test/resource-loader.test.ts:499-577. The CLI resolver feeds both startup and reload; file sources are listed separately from literal prompt text.
func TestResourceLoaderUpstreamSystemPromptSources(t *testing.T) {
	for _, tc := range []struct {
		name, path, content string
		flags               CLIFlags
		wantCustom          string
		wantAppend          string
	}{
		{name: "SYSTEM.md discovery", path: "project/.pig/SYSTEM.md", content: "You are a helpful assistant.", wantCustom: "You are a helpful assistant."},
		{name: "APPEND_SYSTEM.md discovery", path: "project/.pig/APPEND_SYSTEM.md", content: "Additional instructions.", wantAppend: "Additional instructions."},
		{name: "discovered project SYSTEM.md", path: "project/.pig/SYSTEM.md", content: "Project system prompt.", wantCustom: "Project system prompt."},
		{name: "discovered global SYSTEM.md", path: "agent/SYSTEM.md", content: "Global system prompt.", wantCustom: "Global system prompt."},
		{name: "literal system prompt", flags: CLIFlags{SystemPrompt: "Literal system prompt."}, wantCustom: "Literal system prompt."},
		{name: "file-backed system prompt", path: "custom-system.md", content: "Custom system prompt.", flags: CLIFlags{SystemPrompt: "@file"}, wantCustom: "Custom system prompt."},
		{name: "discovered APPEND_SYSTEM.md", path: "project/.pig/APPEND_SYSTEM.md", content: "Project append prompt.", wantAppend: "Project append prompt."},
		{name: "literal append prompt", flags: CLIFlags{AppendSystemPrompt: []string{"Literal append prompt."}}, wantAppend: "Literal append prompt."},
		{name: "mixed append prompts", path: "custom-append.md", content: "Custom append prompt.", flags: CLIFlags{AppendSystemPrompt: []string{"@file", "Literal append prompt."}}, wantAppend: "Custom append prompt.\n\nLiteral append prompt."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
			t.Setenv("PIG_HOME", filepath.Join(root, "home"))
			for _, dir := range []string{cwd, agentDir} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var wantSources []string
			flags := tc.flags
			if tc.path != "" {
				path := filepath.Join(root, tc.path)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
				wantSources = []string{path}
				if flags.SystemPrompt == "@file" {
					flags.SystemPrompt = path
				}
				if len(flags.AppendSystemPrompt) > 0 && flags.AppendSystemPrompt[0] == "@file" {
					flags.AppendSystemPrompt = []string{path, "Literal append prompt."}
				}
			}
			got := resolvePromptInputs(cwd, agentDir, flags, true)
			if got.custom != tc.wantCustom || got.append != tc.wantAppend || !slices.Equal(got.sourcePaths, wantSources) {
				t.Fatalf("resolved prompts = %#v; want custom=%q append=%q sources=%v", got, tc.wantCustom, tc.wantAppend, wantSources)
			}
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			snapshot := reloadResourceSnapshotProvider(cwd, agentDir, sm, flags, nil)()
			if !slices.Equal(snapshot.SystemPromptSourcePaths, wantSources) {
				t.Fatalf("reload sources = %v, want %v", snapshot.SystemPromptSourcePaths, wantSources)
			}
		})
	}
}

func TestLoadContextFilesHonorsDisableFlag(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadContextFiles(cwd, "", true); len(got) != 0 {
		t.Fatalf("disabled context files = %#v", got)
	}
	if got := loadContextFiles(cwd, "", false); len(got) != 1 {
		t.Fatalf("enabled context files = %#v", got)
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:417-429.
func TestResourceLoaderUpstreamNoContextFiles(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{
		"AGENTS.override.md": "# Override Guidelines\n\nBe helpful.",
		"AGENTS.md":          "# Project Guidelines\n\nBe helpful.",
		"CLAUDE.md":          "# Claude Guidelines\n\nBe helpful.",
	} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if files := reloadResourceSnapshotProvider(cwd, agentDir, sm, CLIFlags{NoContextFiles: true}, nil)().ContextFiles; len(files) != 0 {
		t.Fatalf("disabled context files = %#v", files)
	}
}

func TestResolvePromptInputsUsesTrustedProjectBeforeGlobal(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	projectDir := filepath.Join(cwd, ".pig")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(agentDir, "SYSTEM.md"):          "global system",
		filepath.Join(agentDir, "APPEND_SYSTEM.md"):   "global append",
		filepath.Join(projectDir, "SYSTEM.md"):        "project system",
		filepath.Join(projectDir, "APPEND_SYSTEM.md"): "project append",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	trusted := resolvePromptInputs(cwd, agentDir, CLIFlags{}, true)
	if trusted.custom != "project system" || trusted.append != "project append" {
		t.Fatalf("trusted = %#v", trusted)
	}
	untrusted := resolvePromptInputs(cwd, agentDir, CLIFlags{}, false)
	if untrusted.custom != "global system" || untrusted.append != "global append" {
		t.Fatalf("untrusted = %#v", untrusted)
	}
}

func TestResolvePromptInputsReadsExplicitFilesAndSuppressesDiscoveredAppend(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	projectDir := filepath.Join(cwd, ".pig")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "APPEND_SYSTEM.md"), []byte("discovered"), 0o644); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(t.TempDir(), "system.md")
	appendOne := filepath.Join(t.TempDir(), "append-one.md")
	appendTwo := filepath.Join(t.TempDir(), "append-two.md")
	for path, content := range map[string]string{custom: "custom", appendOne: "first", appendTwo: "second"} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := resolvePromptInputs(cwd, agentDir, CLIFlags{SystemPrompt: custom, AppendSystemPrompt: []string{appendOne, appendTwo}}, true)
	if got.custom != "custom" || got.append != "first\n\nsecond" {
		t.Fatalf("resolved = %#v", got)
	}
}
