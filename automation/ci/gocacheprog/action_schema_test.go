// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type compositeAction struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Inputs      map[string]struct {
		Description string `yaml:"description"`
		Required    bool   `yaml:"required"`
		Default     string `yaml:"default"`
	} `yaml:"inputs"`
	Outputs map[string]struct {
		Description string `yaml:"description"`
		Value       string `yaml:"value"`
	} `yaml:"outputs"`
	Runs struct {
		Using string `yaml:"using"`
		Steps []struct {
			Name  string            `yaml:"name"`
			ID    string            `yaml:"id"`
			Shell string            `yaml:"shell"`
			Uses  string            `yaml:"uses"`
			Env   map[string]string `yaml:"env"`
			Run   string            `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"runs"`
}

func TestCompositeActionSchemaAndDefaults(t *testing.T) {
	actionPath := filepath.Join("..", "..", "..", ".github", "actions", "setup-gocacheprog", "action.yml")
	data, err := os.ReadFile(actionPath)
	if err != nil {
		t.Fatalf("read %s: %v", actionPath, err)
	}
	var act compositeAction
	if err := yaml.Unmarshal(data, &act); err != nil {
		t.Fatalf("unmarshal action.yml: %v", err)
	}
	if act.Name == "" || act.Description == "" {
		t.Fatalf("action.yml missing name or description")
	}
	if act.Runs.Using != "composite" {
		t.Fatalf("runs.using = %q, want composite", act.Runs.Using)
	}
	if len(act.Runs.Steps) != 1 {
		t.Fatalf("runs.steps count = %d, want 1", len(act.Runs.Steps))
	}
	step := act.Runs.Steps[0]
	if step.Shell != "bash" {
		t.Errorf("step shell = %q, want bash (portable across Linux, macOS, Windows Git Bash)", step.Shell)
	}
	if step.Uses != "" {
		t.Errorf("composite action step should not pull external unpinned actions: uses=%q", step.Uses)
	}
	if !strings.Contains(step.Run, "automation/ci/setup-gocacheprog.sh") {
		t.Errorf("step run does not invoke automation/ci/setup-gocacheprog.sh: %q", step.Run)
	}
	for _, input := range []string{"mode", "bucket", "endpoint", "region", "prefix", "anonymous", "disk-dir", "verbose"} {
		spec, ok := act.Inputs[input]
		if !ok {
			t.Errorf("missing input %q", input)
			continue
		}
		if spec.Required {
			t.Errorf("input %q must be optional so fork PRs and unconfigured repos fall back cleanly", input)
		}
	}
	if got := act.Inputs["mode"].Default; got != "auto" {
		t.Errorf("inputs.mode.default = %q, want auto", got)
	}
	for _, out := range []string{"enabled", "mode", "gocacheprog"} {
		spec, ok := act.Outputs[out]
		if !ok || !strings.Contains(spec.Value, "steps.configure.outputs."+out) {
			t.Errorf("output %q missing or does not map steps.configure.outputs.%s: %+v", out, out, spec)
		}
	}
}
