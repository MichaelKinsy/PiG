// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type publishWorkflow struct {
	Jobs map[string]struct {
		Strategy struct {
			Matrix struct {
				Include []struct {
					GOOS   string `yaml:"goos"`
					GOARCH string `yaml:"goarch"`
					Format string `yaml:"format"`
				}
			}
		}
		Steps []struct {
			Name string
			Run  string
			If   string
		}
	}
}

func loadPublishWorkflow(t *testing.T) publishWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow publishWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

func TestReleaseCandidateBuildsWindowsARM64Zip(t *testing.T) {
	// Pi's pinned build-binaries.yml includes pi-windows-arm64.zip. A candidate build is not native smoke evidence.
	workflow := loadPublishWorkflow(t)
	for _, target := range workflow.Jobs["binary"].Strategy.Matrix.Include {
		if target.GOOS == "windows" && target.GOARCH == "arm64" {
			if target.Format != "zip" {
				t.Fatalf("windows/arm64 archive format = %q, want zip", target.Format)
			}
			return
		}
	}
	t.Fatal("release candidate binary matrix omits windows/arm64")
}

func TestReleasePublishConditionsUseSupportedContexts(t *testing.T) {
	workflow := loadPublishWorkflow(t)
	for _, step := range workflow.Jobs["publish"].Steps {
		if strings.Contains(step.If, "secrets.") {
			t.Errorf("%s references secrets directly in if: %s", step.Name, step.If)
		}
	}
}
