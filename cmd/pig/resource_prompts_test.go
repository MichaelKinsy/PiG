package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
