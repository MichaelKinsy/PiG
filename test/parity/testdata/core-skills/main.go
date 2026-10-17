package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	root := os.Args[1]
	paths := []string{}
	for _, tc := range []struct{ name, fields string }{{"invalid", "description: true"}, {"renamed", "name: true\ndescription: valid"}, {"enabled", "description: valid\ndisable-model-invocation: \"true\""}} {
		dir := filepath.Join(root, tc.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+tc.fields+"\n---\nbody"), 0o600); err != nil {
			return err
		}
		paths = append(paths, dir)
	}
	result, err := codingagent.LoadSkills(codingagent.LoadSkillsOptions{CWD: root, AgentDir: filepath.Join(root, "agent"), SkillPaths: paths})
	if err != nil {
		return err
	}
	records := []any{}
	for _, skill := range result.Skills {
		path, err := filepath.Rel(root, skill.FilePath)
		if err != nil {
			return err
		}
		records = append(records, []any{"skill", skill.Name, skill.Description, skill.DisableModelInvocation, filepath.ToSlash(path), skill.SourceInfo.Source, skill.SourceInfo.Scope})
	}
	for _, warning := range result.Diagnostics {
		path, err := filepath.Rel(root, warning.Path)
		if err != nil {
			return err
		}
		records = append(records, []any{"warning", warning.Type, warning.Message, filepath.ToSlash(path)})
	}
	return json.NewEncoder(os.Stdout).Encode(records)
}
