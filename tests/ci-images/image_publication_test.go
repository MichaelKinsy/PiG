// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type imageWorkflowStep struct {
	Name string
	ID   string `yaml:"id"`
	Uses string
	If   string
	Run  string
	With map[string]string
}

type imageWorkflow struct {
	Permissions map[string]string
	Jobs        map[string]struct {
		If          string
		Needs       string
		Permissions map[string]string
		Environment struct{ Name string }
		Steps       []imageWorkflowStep
	}
}

func loadImageWorkflow(t *testing.T) imageWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/ci-images.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow imageWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

func imageStep(t *testing.T, workflow imageWorkflow, job, name string) imageWorkflowStep {
	t.Helper()
	for _, step := range workflow.Jobs[job].Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("%s: missing %s step", job, name)
	return imageWorkflowStep{}
}

func TestCIImagePublicationIsEnvironmentProtected(t *testing.T) {
	workflow := loadImageWorkflow(t)
	for scope, permission := range workflow.Permissions {
		if permission != "read" && permission != "none" {
			t.Errorf("default %s permission is %s", scope, permission)
		}
	}
	for job, config := range workflow.Jobs {
		if job == "publish" {
			continue
		}
		for scope, permission := range config.Permissions {
			if permission != "read" && permission != "none" {
				t.Errorf("%s has %s: %s outside publication", job, scope, permission)
			}
		}
		for _, step := range config.Steps {
			if strings.HasPrefix(step.Uses, "docker/login-action@") || strings.Contains(step.Run, "docker push") {
				t.Errorf("%s can publish outside the protected job", job)
			}
		}
	}
	publish := workflow.Jobs["publish"]
	if publish.Environment.Name != "release" || publish.Permissions["packages"] != "write" || publish.Needs != "build" {
		t.Fatal("publication must depend on all builds and own packages: write behind the release environment")
	}
	const gate = "github.event_name == 'workflow_dispatch' && inputs.publish == true && github.ref == 'refs/heads/main'"
	if publish.If != gate {
		t.Fatalf("publication gate = %q, want %q", publish.If, gate)
	}
	upload := imageStep(t, workflow, "build", "Upload validated image")
	download := imageStep(t, workflow, "publish", "Download validated image")
	if upload.If != gate || upload.With["name"] == "" || upload.With["name"] != download.With["name"] {
		t.Fatal("publication must download the matching dispatch-only build artifact")
	}
	if upload.With["if-no-files-found"] != "error" {
		t.Fatal("missing image archive must fail the build")
	}
}

func TestCIImagePublicationTransfersValidatedBytes(t *testing.T) {
	workflow := loadImageWorkflow(t)
	save := imageStep(t, workflow, "build", "Save validated image").Run
	load := imageStep(t, workflow, "publish", "Verify and load validated image").Run
	publish := imageStep(t, workflow, "publish", "Publish immutable image").Run
	for _, mode := range []string{"absent", "corrupt", "wrong-image", "exists", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("DOCKER_CONFIG", filepath.Join(dir, "docker-config"))
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("CANDIDATE", filepath.Join(dir, "candidate"))
			t.Setenv("GITHUB_OUTPUT", filepath.Join(dir, "output"))
			t.Setenv("IMAGE_REF", "registry.test/ci:commit")
			t.Setenv("DOCKER_LOG", filepath.Join(dir, "docker.log"))
			t.Setenv("MODE", mode)
			fake := `#!/bin/bash
set -eu
printf '%s\n' "$*" >> "$DOCKER_LOG"
case "$*" in
  'image save registry.test/ci:commit') printf 'validated image bytes' ;;
  'image inspect --format {{.Id}} registry.test/ci:commit')
    if [ "$MODE" = wrong-image ] && [ -f loaded ]; then echo sha256:wrong; else echo sha256:validated; fi ;;
  'load --input image.tar.gz')
    test "$(gzip -dc image.tar.gz)" = 'validated image bytes'
    touch loaded ;;
  'manifest inspect registry.test/ci:commit')
    case "$MODE" in
      exists) exit 0 ;;
      unavailable) echo 'denied: unavailable' >&2 ;;
      *) echo 'manifest unknown' >&2 ;;
    esac
    exit 1 ;;
  'push registry.test/ci:commit') echo 'commit: digest: sha256:published size: 123' ;;
  *) echo "unexpected docker invocation: $*" >&2; exit 2 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fake), 0o700); err != nil {
				t.Fatal(err)
			}
			run := func(command, cwd string) ([]byte, error) {
				cmd := exec.Command(testenv.Bash(t), "-c", command)
				cmd.Dir = cwd
				return cmd.CombinedOutput()
			}
			if output, err := run(save, dir); err != nil {
				t.Fatalf("save: %v\n%s", err, output)
			}
			candidate := os.Getenv("CANDIDATE")
			if mode == "corrupt" {
				if err := os.WriteFile(filepath.Join(candidate, "image.tar.gz"), []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			output, err := run(load, candidate)
			if mode == "corrupt" || mode == "wrong-image" {
				if err == nil {
					t.Fatalf("load accepted %s image", mode)
				}
			} else {
				if err != nil {
					t.Fatalf("load: %v\n%s", err, output)
				}
				output, err = run(publish, candidate)
				if (err == nil) != (mode == "absent") {
					t.Fatalf("publish %s: %v\n%s", mode, err, output)
				}
			}
			log, err := os.ReadFile(os.Getenv("DOCKER_LOG"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(log), "push registry.test/") != (mode == "absent") {
				t.Fatalf("unexpected publication in %s: %s", mode, log)
			}
			if mode == "corrupt" && strings.Contains(string(log), "load --input") {
				t.Fatal("corrupt archive reached docker load before checksum verification")
			}
		})
	}
}
