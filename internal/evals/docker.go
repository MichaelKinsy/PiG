package evals

// Ports packages/evals/src/docker.ts.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Command runs one external command and reports its exit status. Capture returns stdout and leaves stderr attached to
// the caller; otherwise all three streams stay attached.
type Command interface {
	Execute(ctx context.Context, name string, args []string, capture bool) (status int, stdout string, err error)
}

// ExecCommand runs commands with os/exec in Dir.
type ExecCommand struct{ Dir string }

// Execute implements Command. A command that cannot start is an error; one that ends without an exit status counts as
// status 1.
func (command ExecCommand) Execute(ctx context.Context, name string, args []string, capture bool) (int, string, error) {
	process := exec.CommandContext(ctx, name, args...)
	process.Dir = command.Dir
	process.Stderr = os.Stderr
	var output strings.Builder
	if capture {
		process.Stdout = &output
	} else {
		process.Stdin, process.Stdout = os.Stdin, os.Stdout
	}
	err := process.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, output.String(), nil
	case errors.As(err, &exit):
		if status := exit.ExitCode(); status >= 0 {
			return status, output.String(), nil
		}
		return 1, output.String(), nil
	default:
		return 0, "", err
	}
}

// BuiltImage names one documentation-variant image and its content ID.
type BuiltImage struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// BuiltImages holds the image of each documentation variant.
type BuiltImages struct {
	WithoutDocs BuiltImage `json:"without_docs"`
	WithDocs    BuiltImage `json:"with_docs"`
}

// Variant returns the image of variant.
func (images BuiltImages) Variant(variant DocumentationVariant) BuiltImage {
	if variant == DocumentationVariantWithDocs {
		return images.WithDocs
	}
	return images.WithoutDocs
}

// DockerContext carries what every container run needs.
type DockerContext struct {
	Command           Command
	PackageRoot       string
	Images            BuiltImages
	ArtifactDirectory string
	AuthPath          string
	Provider          string
	Model             string
	RunsPerVariant    int
}

func requireSuccess(ctx context.Context, command Command, name string, args ...string) error {
	status, _, err := command.Execute(ctx, name, args, false)
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("%s exited with status %d.", name, status)
	}
	return nil
}

// BuildImages builds the without_docs and with_docs images from the Dockerfile under packageRoot with
// repositoryRoot as the build context, then reads each image ID.
func BuildImages(ctx context.Context, command Command, packageRoot, repositoryRoot string) (BuiltImages, error) {
	digest := sha256.Sum256([]byte(repositoryRoot))
	prefix := "pi-evals-" + hex.EncodeToString(digest[:])[:12]
	images := BuiltImages{
		WithoutDocs: BuiltImage{Name: prefix + "-without-docs:local"},
		WithDocs:    BuiltImage{Name: prefix + "-with-docs:local"},
	}
	for _, variant := range DocumentationVariants {
		image := images.Variant(variant)
		if err := requireSuccess(ctx, command, "docker", "build", "--target", string(variant), "--tag", image.Name,
			"--file", filepath.Join(packageRoot, "docker", "Dockerfile"), repositoryRoot); err != nil {
			return BuiltImages{}, err
		}
		status, stdout, err := command.Execute(ctx, "docker", []string{"image", "inspect", "--format", "{{.Id}}", image.Name}, true)
		if err != nil {
			return BuiltImages{}, err
		}
		if status != 0 || jsstring.Trim(stdout) == "" {
			return BuiltImages{}, fmt.Errorf("Cannot inspect %s.", image.Name)
		}
		image.ID = jsstring.Trim(stdout)
		if variant == DocumentationVariantWithDocs {
			images.WithDocs = image
		} else {
			images.WithoutDocs = image
		}
	}
	return images, nil
}

func environment(name, value string) []string { return []string{"--env", name + "=" + value} }

// RequireEvalAuthFile returns the path of the agent directory's auth.json after checking that it is a JSON object
// with a credential for provider.
func RequireEvalAuthFile(provider string) (string, error) {
	// pig divergence (D2): PiG reads its own agent directory variable and defaults to ~/.pig/agent.
	agentDir := icodingagent.DefaultAgentDir()
	if configured := os.Getenv(agentDirEnvironment()); configured != "" {
		var err error
		if agentDir, err = filepath.Abs(configured); err != nil {
			return "", err
		}
	}
	path := filepath.Join(agentDir, "auth.json")
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("Eval authentication file does not exist: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var credentials any
	if err := json.Unmarshal(data, &credentials); err != nil {
		return "", fmt.Errorf("Eval authentication file is invalid: %s: %w", path, err)
	}
	object, isObject := credentials.(map[string]any)
	if _, hasProvider := object[provider]; !isObject || !hasProvider {
		return "", fmt.Errorf("Eval authentication file has no credential for provider %s.", provider)
	}
	return path, nil
}

func dockerArgs(context DockerContext, variant DocumentationVariant, outputDirectory string, entrypointArgs []string) ([]string, error) {
	if err := os.MkdirAll(outputDirectory, 0o700); err != nil {
		return nil, err
	}
	args := []string{"run", "--rm", "--read-only", "--tmpfs", "/tmp:rw,exec,mode=1777", "--mount", "type=bind,source=" + outputDirectory + ",target=/artifacts"}
	for _, variable := range [][2]string{
		{"PI_EVAL_ARTIFACT_DIR", "/artifacts"},
		{"PI_EVAL_RUNS_PER_VARIANT", strconv.Itoa(context.RunsPerVariant)},
		{"PI_EVAL_SANDBOX_UID", "65532"},
		{"PI_EVAL_SANDBOX_GID", "65532"},
		{"PI_PROVIDER", context.Provider},
		{"PI_MODEL", context.Model},
	} {
		args = append(args, environment(variable[0], variable[1])...)
	}
	if runtime.GOOS != "windows" {
		args = append(args, environment("PI_EVAL_ARTIFACT_UID", strconv.Itoa(os.Getuid()))...)
		args = append(args, environment("PI_EVAL_ARTIFACT_GID", strconv.Itoa(os.Getgid()))...)
	}
	args = append(args, "--mount", "type=bind,source="+context.AuthPath+",target=/run/pi-eval-secrets/auth.json,readonly")
	args = append(args, context.Images.Variant(variant).Name)
	return append(args, entrypointArgs...), nil
}

// DiscoverCases lists the cases of files in the variant's image and returns the path of the discovery report.
func DiscoverCases(ctx context.Context, docker DockerContext, variant DocumentationVariant, files, vitestArgs []string) (string, error) {
	outputDirectory := filepath.Join(docker.ArtifactDirectory, "discovery", string(variant))
	entrypointArgs := append(append([]string{"--discover", "--project", "docs"}, files...), vitestArgs...)
	args, err := dockerArgs(docker, variant, outputDirectory, entrypointArgs)
	if err != nil {
		return "", err
	}
	status, _, err := docker.Command.Execute(ctx, "docker", args, false)
	if err != nil {
		return "", err
	}
	if status != 0 {
		return "", fmt.Errorf("%s eval discovery failed.", variant)
	}
	return filepath.Join(outputDirectory, "discovered-tests.json"), nil
}

func taskDirectoryName(task EvalTask) (string, error) {
	digest, err := taskIdentityDigest(task)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest[:]), nil
}

var regexpSpecials = regexp.MustCompile(`[.*+?^${}()|\[\]\\]`)

// RunTask runs one isolated task in a fresh container. A failed run is not an error: the result is the path of the
// Vitest report when the run wrote one, and "" otherwise.
func RunTask(ctx context.Context, docker DockerContext, task EvalTask) (string, error) {
	directoryName, err := taskDirectoryName(task)
	if err != nil {
		return "", err
	}
	outputDirectory := filepath.Join(docker.ArtifactDirectory, "tasks", directoryName)
	exactName := "^" + regexpSpecials.ReplaceAllString(task.EvalSet+" "+task.CaseID, `\$0`) + "$"
	args, err := dockerArgs(docker, task.Variant, outputDirectory, []string{"--project", "docs", task.File, "--testNamePattern", exactName})
	if err != nil {
		return "", err
	}
	if _, _, err := docker.Command.Execute(ctx, "docker", args, false); err != nil {
		return "", err
	}
	reportPath := filepath.Join(outputDirectory, "vitest.json")
	if _, err := os.Stat(reportPath); err != nil {
		return "", nil
	}
	return reportPath, nil
}
