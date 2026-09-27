package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/text"
)

type resolvedPromptInputs struct {
	custom string
	append string
	// sourcePaths are the existing files the custom and then each append
	// input came from, as upstream reports getSystemPromptSource and
	// getAppendSystemPromptSources.
	sourcePaths []string
}

// resolvePromptInputs applies the resource-loader precedence shared by every
// mode: explicit CLI values, then a trusted project file, then a global file.
func resolvePromptInputs(cwd, agentDir string, flags CLIFlags, projectTrusted bool) resolvedPromptInputs {
	customSource := flags.SystemPrompt
	if customSource == "" {
		customSource = discoverPromptFile(cwd, agentDir, "SYSTEM.md", projectTrusted)
	}
	appendSources := flags.AppendSystemPrompt
	if len(appendSources) == 0 {
		if discovered := discoverPromptFile(cwd, agentDir, "APPEND_SYSTEM.md", projectTrusted); discovered != "" {
			appendSources = []string{discovered}
		}
	}
	resolvedAppend := make([]string, 0, len(appendSources))
	for _, source := range appendSources {
		if value := resolvePromptInput(source, "append system prompt"); value != "" {
			resolvedAppend = append(resolvedAppend, value)
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
	if projectTrusted {
		projectPath := filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, name)
		if _, err := os.Stat(projectPath); err == nil {
			return projectPath
		}
	}
	globalPath := filepath.Join(agentDir, name)
	if _, err := os.Stat(globalPath); err == nil {
		return globalPath
	}
	return ""
}

func resolvePromptInput(input, description string) string {
	if input == "" {
		return ""
	}
	if _, err := os.Stat(input); err != nil {
		return input
	}
	data, err := os.ReadFile(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read %s file %s: %v\n", description, input, err)
		return input
	}
	return text.StripBom(string(data))
}
