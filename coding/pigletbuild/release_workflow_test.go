package pigletbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// releaseWorkflow is the part of a GitHub Actions workflow the example's
// contract depends on.
type releaseWorkflow struct {
	On struct {
		WorkflowCall struct {
			Inputs map[string]struct {
				Required bool   `yaml:"required"`
				Type     string `yaml:"type"`
				Default  string `yaml:"default"`
			} `yaml:"inputs"`
			Secrets map[string]struct {
				Required bool `yaml:"required"`
			} `yaml:"secrets"`
		} `yaml:"workflow_call"`
	} `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		Needs       any               `yaml:"needs"`
		RunsOn      string            `yaml:"runs-on"`
		Permissions map[string]string `yaml:"permissions"`
		Steps       []struct {
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]any    `yaml:"with"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

var (
	pinnedAction      = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._/-]+@[0-9a-f]{40}$`)
	workflowReference = regexp.MustCompile(`\b(inputs|secrets|matrix)\.([A-Za-z0-9_-]+)`)
	workflowExpr      = regexp.MustCompile(`\$\{\{(.*?)\}\}`)
)

func readReleaseWorkflow(t *testing.T) (releaseWorkflow, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "piglet-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow releaseWorkflow
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&workflow); err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	return workflow, string(data)
}

// The example is a reusable workflow whose token defaults to read-only, whose
// actions are pinned to full commit SHAs, and whose every expression names a
// declared input, secret, or matrix field.
func TestPigletReleaseWorkflowIsPinnedReusableAndDeclared(t *testing.T) {
	workflow, text := readReleaseWorkflow(t)
	call := workflow.On.WorkflowCall
	for _, name := range []string{"piglet", "pig-ref"} {
		if input, ok := call.Inputs[name]; !ok || !input.Required || input.Type != "string" {
			t.Errorf("workflow_call input %q = %+v, want a required string", name, input)
		}
	}
	if secret, ok := call.Secrets["piglet-signing-key"]; !ok || !secret.Required {
		t.Error("workflow_call must require the piglet-signing-key secret")
	}
	if len(workflow.Permissions) != 1 || workflow.Permissions["contents"] != "read" {
		t.Errorf("top-level permissions = %v, want contents: read", workflow.Permissions)
	}
	var targets []map[string]string
	if err := json.Unmarshal([]byte(call.Inputs["targets"].Default), &targets); err != nil || len(targets) == 0 {
		t.Fatalf("default targets %q: %v", call.Inputs["targets"].Default, err)
	}
	matrix := map[string]bool{}
	for _, target := range targets {
		if _, err := parsePublishTarget(target["goos"] + "/" + target["goarch"]); err != nil || target["runner"] == "" || len(target) != 3 {
			t.Errorf("default target %v must name goos, goarch, and runner", target)
		}
		for key := range target {
			matrix[key] = true
		}
	}
	// The header comment shows the caller's own workflow, which is not
	// evaluated in this file's contexts.
	var body strings.Builder
	for line := range strings.SplitSeq(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			body.WriteString(line + "\n")
		}
	}
	for _, expression := range workflowExpr.FindAllStringSubmatch(body.String(), -1) {
		for _, reference := range workflowReference.FindAllStringSubmatch(expression[1], -1) {
			_, input := call.Inputs[reference[2]]
			_, secret := call.Secrets[reference[2]]
			declared := map[string]bool{"inputs": input, "secrets": secret, "matrix": matrix[reference[2]]}[reference[1]]
			if !declared {
				t.Errorf("expression %q references undeclared %s.%s", expression[0], reference[1], reference[2])
			}
		}
	}
	if needs, _ := workflow.Jobs["publish"].Needs.(string); needs != "build" || len(workflow.Jobs) != 2 {
		t.Errorf("jobs = %v; publish must need build", slices.Sorted(func(yield func(string) bool) {
			for name := range workflow.Jobs {
				if !yield(name) {
					return
				}
			}
		}))
	}
	if permissions := workflow.Jobs["build"].Permissions; permissions["id-token"] != "write" || permissions["attestations"] != "write" || permissions["contents"] != "read" {
		t.Errorf("build permissions = %v, want provenance attestation only", permissions)
	}
	if permissions := workflow.Jobs["publish"].Permissions; len(permissions) != 1 || permissions["contents"] != "write" {
		t.Errorf("publish permissions = %v, want contents: write only", permissions)
	}
	for name, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if step.Uses != "" && !pinnedAction.MatchString(step.Uses) {
				t.Errorf("job %s uses %s, which is not pinned to a full commit SHA", name, step.Uses)
			}
		}
	}
}

// The workflow's pig commands parse with the current flags, build into the
// directory it uploads, and publish from the directory it downloads.
func TestPigletReleaseWorkflowRunsCurrentPigCommands(t *testing.T) {
	workflow, _ := readReleaseWorkflow(t)
	build := workflowCommand(t, workflow, "build", "piglet build")
	if _, opts, _, out, err := parseArgs(build); err != nil || opts.Format != "binary" || opts.Builder != "native" || opts.SignKeyPath == "" || !strings.HasPrefix(out, "dist/") {
		t.Fatalf("build job runs pig piglet build %q: opts=%+v out=%q err=%v", build, opts, out, err)
	}
	publish := workflowCommand(t, workflow, "publish", "piglet publish")
	request, help, err := parsePublishArgs(publish)
	if err == nil {
		err = request.validate()
	}
	if err != nil || help || !request.yes || request.artifacts != "dist" || request.commit == "" || request.signKeyPath == "" {
		t.Fatalf("publish job runs pig piglet publish %q: request=%+v err=%v", publish, request, err)
	}
	var uploaded, downloaded string
	for _, step := range workflow.Jobs["build"].Steps {
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			uploaded, _ = step.With["path"].(string)
		}
	}
	for _, step := range workflow.Jobs["publish"].Steps {
		if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
			downloaded, _ = step.With["path"].(string)
			if merge, _ := step.With["merge-multiple"].(bool); !merge {
				t.Error("download-artifact must merge every target's Binary into one --artifacts directory")
			}
		}
	}
	if uploaded != "dist/*" || downloaded != request.artifacts {
		t.Fatalf("build uploads %q and publish downloads %q; want dist/* and %q", uploaded, downloaded, request.artifacts)
	}
}

// workflowCommand returns the arguments after `pig <marker>` in the one run
// step of job that invokes it, with GitHub-provided variables replaced by
// representative values.
func workflowCommand(t *testing.T, workflow releaseWorkflow, job, marker string) []string {
	t.Helper()
	var found []string
	for _, step := range workflow.Jobs[job].Steps {
		script := strings.ReplaceAll(step.Run, "\\\n", " ")
		for line := range strings.SplitSeq(script, "\n") {
			_, after, ok := strings.Cut(line, marker+" ")
			if !ok {
				continue
			}
			if found != nil {
				t.Fatalf("job %s runs pig %s more than once", job, marker)
			}
			after = strings.NewReplacer(
				"$GITHUB_REPOSITORY", "acme/porter",
				"$GITHUB_SHA", strings.Repeat("a", 40),
			).Replace(after)
			found = shellWords(after)
		}
	}
	if found == nil {
		t.Fatalf("job %s never runs pig %s", job, marker)
	}
	return found
}

// shellWords splits the simple quoted words the workflow's run steps use.
func shellWords(line string) []string {
	var words []string
	var word strings.Builder
	inWord := false
	var quote rune
	for _, char := range line {
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(char)
		case char == '"' || char == '\'':
			quote, inWord = char, true
		case char == ' ' || char == '\t':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(char)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}
