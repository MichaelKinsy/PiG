package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/env"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	root := os.Args[1]
	e := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})
	ctx := context.Background()
	for path, content := range map[string]string{
		"prompts/one.md":            "---\ndescription: One\n---\nHello $1",
		"prompts/nested/ignored.md": "Ignored",
		"skills/example/SKILL.md":   "---\nname: example\ndescription: Example\n---\nUse this skill.",
		"broken/SKILL.md":           "---\nname: broken\n---\nMissing description.",
	} {
		if err := e.WriteFile(ctx, path, []byte(content)); err != nil {
			return err
		}
	}
	prompts, pw, err := harness.LoadSourcedPromptTemplates(ctx, e, []harness.SourcedPath[string]{{Path: "prompts", Source: "project"}}, nil)
	if err != nil {
		return err
	}
	skills, sw, err := harness.LoadSourcedSkills(ctx, e, []harness.SourcedPath[string]{{Path: "skills", Source: "user"}, {Path: "broken", Source: "external"}}, nil)
	if err != nil {
		return err
	}
	records := []any{}
	for _, item := range prompts {
		template, ok := item.PromptTemplate.(harness.PromptTemplate)
		if !ok {
			return fmt.Errorf("unexpected mapped template %T", item.PromptTemplate)
		}
		records = append(records, []any{"prompt", template.Name, template.Description, template.Content, item.Source, harness.FormatPromptTemplateInvocation(template, []string{"world"})})
	}
	for _, item := range skills {
		skill, ok := item.Skill.(harness.Skill)
		if !ok {
			return fmt.Errorf("unexpected mapped skill %T", item.Skill)
		}
		path, err := filepath.Rel(root, skill.FilePath)
		if err != nil {
			return err
		}
		records = append(records, []any{"skill", skill.Name, skill.Description, skill.Content, filepath.ToSlash(path), skill.DisableModelInvocation, item.Source})
	}
	if len(pw) != 0 {
		return fmt.Errorf("unexpected prompt diagnostics: %v", pw)
	}
	for _, warning := range sw {
		path, err := filepath.Rel(root, warning.Path)
		if err != nil {
			return err
		}
		records = append(records, []any{"warning", warning.Type, warning.Code, warning.Message, filepath.ToSlash(path), warning.Source})
	}
	return json.NewEncoder(os.Stdout).Encode(records)
}
