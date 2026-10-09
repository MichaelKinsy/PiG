// Ports packages/coding-agent/src/core/resource-loader.ts
package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type resolvedPromptInputs struct {
	custom    string
	customSet bool
	append    string
	// sourcePaths are the existing files the custom and then each append
	// input came from, as upstream reports getSystemPromptSource and
	// getAppendSystemPromptSources.
	sourcePaths []string
}

// resolvePromptInputs applies the resource-loader precedence shared by every mode: explicit CLI values, then a trusted project file, then a global file. An explicit empty append list suppresses discovery, and empty file contents remain append entries.
func resolvePromptInputs(cwd, agentDir string, flags Args, projectTrusted bool) resolvedPromptInputs {
	customSource := flags.SystemPrompt
	if customSource == "" && !flags.systemPromptSet {
		customSource = discoverPromptFile(cwd, agentDir, "SYSTEM.md", projectTrusted)
	}
	appendSources := flags.AppendSystemPrompt
	if appendSources == nil {
		if discovered := discoverPromptFile(cwd, agentDir, "APPEND_SYSTEM.md", projectTrusted); discovered != "" {
			appendSources = []string{discovered}
		}
	}
	resolvedAppend := make([]string, 0, len(appendSources))
	for _, source := range appendSources {
		if source != "" {
			resolvedAppend = append(resolvedAppend, resolvePromptInput(source, "append system prompt"))
		}
	}
	var sourcePaths []string
	for _, source := range append([]string{customSource}, appendSources...) {
		if source == "" {
			continue
		}
		if _, err := os.Stat(source); err == nil {
			if absolute, err := filepath.Abs(source); err == nil {
				source = absolute
			}
			sourcePaths = append(sourcePaths, source)
		}
	}
	return resolvedPromptInputs{
		custom:      resolvePromptInput(customSource, "system prompt"),
		customSet:   customSource != "",
		append:      strings.Join(resolvedAppend, "\n\n"),
		sourcePaths: sourcePaths,
	}
}

func loadContextFiles(cwd, agentDir string, disabled bool) []codingagent.ContextFile {
	if disabled {
		return nil
	}
	return codingagent.LoadProjectContextFiles(cwd, agentDir)
}

func discoverPromptFile(cwd, agentDir, name string, projectTrusted bool) string {
	return codingagent.DiscoverPromptFile(cwd, agentDir, name, projectTrusted)
}

func resolvePromptInput(input, description string) string {
	return codingagent.ResolvePromptInput(input, description)
}
