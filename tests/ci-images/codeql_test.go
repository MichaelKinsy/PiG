// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
package ciimages

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestCodeQLCoversGoAndInterpretedSDKs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Include []struct {
						Language  string
						BuildMode string `yaml:"build-mode"`
						Config    string
					}
				}
			}
			Steps []imageWorkflowStep
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	wantPaths := map[string][]string{
		"go":                    nil,
		"javascript-typescript": {"extensions/sdk-ts", "coding/extension/host/subprocess/runtime-node"},
		"python":                {"extensions/sdk-py"},
	}
	job := workflow.Jobs["codeql"]
	seen := make(map[string]bool)
	for _, target := range job.Strategy.Matrix.Include {
		paths, ok := wantPaths[target.Language]
		if !ok || seen[target.Language] {
			t.Fatalf("unexpected or duplicate CodeQL language %q", target.Language)
		}
		seen[target.Language] = true
		mode := "none"
		if target.Language == "go" {
			mode = "manual"
		}
		if target.BuildMode != mode {
			t.Errorf("%s build-mode = %q, want %q", target.Language, target.BuildMode, mode)
		}
		var config struct{ Paths []string }
		if err := yaml.Unmarshal([]byte(target.Config), &config); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(config.Paths, paths) {
			t.Errorf("%s paths = %v, want %v", target.Language, config.Paths, paths)
		}
		for _, path := range config.Paths {
			if _, err := os.Stat(filepath.Join(repoRoot(t), path)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for language := range wantPaths {
		if !seen[language] {
			t.Errorf("CodeQL matrix omits %s", language)
		}
	}
	var initialized, built, analyzed bool
	for _, step := range job.Steps {
		switch {
		case strings.HasPrefix(step.Uses, "github/codeql-action/init@"):
			initialized = true
			for key, want := range map[string]string{
				"languages":  "${{ matrix.language }}",
				"build-mode": "${{ matrix.build-mode }}",
				"config":     "${{ matrix.config }}",
			} {
				if step.With[key] != want {
					t.Errorf("CodeQL init %s = %q, want %q", key, step.With[key], want)
				}
			}
		case strings.HasPrefix(step.Uses, "actions/setup-go@"):
			if step.If != "matrix.language == 'go'" {
				t.Error("interpreted-language analysis must not require Go setup")
			}
		case step.Run == "go build ./...":
			built = true
			if !initialized || step.If != "matrix.build-mode == 'manual'" {
				t.Error("manual build must run after init and only for the compiled language")
			}
		case strings.HasPrefix(step.Uses, "github/codeql-action/analyze@"):
			analyzed = true
			if !built || step.With["category"] != "/language:${{ matrix.language }}" {
				t.Error("analysis must follow the build and use a distinct language category")
			}
		}
	}
	if !initialized || !built || !analyzed {
		t.Fatal("CodeQL must initialize, build Go, and analyze")
	}
}
