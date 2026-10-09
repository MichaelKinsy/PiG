//go:build unix

package evals

// Ports packages/evals/docker/entrypoint.ts.
//
// Pi's entrypoint checks the installed workspace, the evaluator sources' permissions and the dist resolution of
// pi-coding-agent, prepares the agent directory, and starts Vitest. PiG's image holds compiled binaries, so the
// workspace check covers them, the evaluator is the root-only runner binary itself, and the suites run in this
// process instead of a Vitest child. The 1.0.4 change to entrypoint.ts (no npm-shrinkwrap.json check) has no
// counterpart: PiG's image has no npm install tree.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// EntrypointConfig locates the image's contents.
type EntrypointConfig struct {
	// PackageRoot is the evals package directory as discovered files report it.
	PackageRoot string
	// Runner is the root-only evaluator binary.
	Runner string
	// Binaries are the executables the sandboxed agent runs; each must exist.
	Binaries []string
	// AuthSource is the credential file the host mounts.
	AuthSource string
	// HostAgentDir receives the credentials for the harness.
	HostAgentDir string
	// ArtifactDir is where reports are written.
	ArtifactDir string
	Suites      Runner
}

func parseEntrypointID(name string) (int, error) {
	value := jsnumber.Parse(os.Getenv(name))
	if value != float64(int64(value)) || value < 1 || value > maxSafeInteger {
		return 0, fmt.Errorf("%s must be a positive integer.", name)
	}
	return int(value), nil
}

func assertWorkspace(config EntrypointConfig, variant string) error {
	for _, binary := range config.Binaries {
		info, err := os.Stat(binary)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			return fmt.Errorf("Installed binary is missing or not executable: %s", binary)
		}
	}
	return assertDocumentation(variant)
}

// assertDocumentation is entrypoint.ts:48-62 over what PiG's agent sees. A pig run materializes its embedded
// documentation under the config root it starts with, so the check materializes a fresh root, applies the variant's
// removal, and then asserts the result: without_docs leaves no README.md, CHANGELOG.md, docs or examples; with_docs
// leaves README.md and models.md in docs, non-empty, with every embedded page present. PiG's bundle has no
// CHANGELOG.md or examples directory (D22), so with_docs cannot require them.
func assertDocumentation(variant string) error {
	root, err := os.MkdirTemp("", "pig-eval-docs-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	if _, err := pigdocs.Sync(root); err != nil {
		return err
	}
	switch DocumentationVariant(variant) {
	case DocumentationVariantWithoutDocs:
		if err := removePigDocumentation(root); err != nil {
			return err
		}
		for _, name := range []string{"README.md", "CHANGELOG.md", "docs", "examples"} {
			if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
				return fmt.Errorf("without_docs contains pig %s.", name)
			}
		}
		return nil
	case DocumentationVariantWithDocs:
		docs := pigdocs.DocsDir(root)
		names := pigdocs.List()
		for _, required := range []string{"README.md", "models.md"} {
			if !slices.Contains(names, required) {
				return fmt.Errorf("Missing documentation: %s", filepath.Join(docs, required))
			}
		}
		for _, name := range names {
			path := filepath.Join(docs, name)
			if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
				return fmt.Errorf("Missing documentation: %s", path)
			}
		}
		return nil
	}
	return errors.New("Eval image has no valid PI_EVAL_VARIANT.")
}

func assertRootOnly(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("Evaluator source must be readable only by root: %s", path)
	}
	return nil
}

// assertSandboxCannotRead runs `test -r path` as the sandbox identity; success means the model-facing user can read it.
func assertSandboxCannotRead(ctx context.Context, path string, uid, gid int) error {
	probe := exec.CommandContext(ctx, "test", "-r", path)
	probe.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}}
	if probe.Run() == nil {
		return fmt.Errorf("The model-facing user can read evaluator source: %s", path)
	}
	return nil
}

var testNameOption = regexp.MustCompile(`^(?:-t|--testNamePattern)$`)

// ParseEntrypointArgs reads the arguments the host passes (docker.ts discoverCases and runTask): --discover,
// --project, eval files and a test name pattern.
func ParseEntrypointArgs(args []string) (discover bool, selection Selection, err error) {
	selection.Project = EvalProjectDocs
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--discover":
			discover = true
		case argument == "--project":
			index++
			if index >= len(args) {
				return false, Selection{}, errors.New("Missing value for --project.")
			}
			selection.Project = EvalProject(args[index])
		case testNameOption.MatchString(argument):
			index++
			if index >= len(args) {
				return false, Selection{}, fmt.Errorf("Missing value for %s.", argument)
			}
			if selection.NamePattern, err = compileNamePattern(args[index]); err != nil {
				return false, Selection{}, err
			}
		case strings.HasPrefix(argument, "--testNamePattern="):
			if selection.NamePattern, err = compileNamePattern(strings.TrimPrefix(argument, "--testNamePattern=")); err != nil {
				return false, Selection{}, err
			}
		case strings.HasSuffix(argument, DocsEvalSuffix):
			selection.Files = append(selection.Files, argument)
		default:
			return false, Selection{}, fmt.Errorf("Unsupported container argument: %s", argument)
		}
	}
	return discover, selection, nil
}

// RunEntrypoint runs the container entry point: it checks the image, prepares the agent directory, lists or runs the
// selected suites and returns the process exit code.
func RunEntrypoint(ctx context.Context, config EntrypointConfig, args []string, stdout io.Writer) (int, error) {
	variant := os.Getenv("PI_EVAL_VARIANT")
	uid, err := parseEntrypointID("PI_EVAL_SANDBOX_UID")
	if err != nil {
		return 0, err
	}
	gid, err := parseEntrypointID("PI_EVAL_SANDBOX_GID")
	if err != nil {
		return 0, err
	}
	if err := assertWorkspace(config, variant); err != nil {
		return 0, err
	}
	if err := assertRootOnly(config.Runner); err != nil {
		return 0, err
	}
	if err := assertSandboxCannotRead(ctx, config.Runner, uid, gid); err != nil {
		return 0, err
	}

	if err := os.MkdirAll(config.HostAgentDir, 0o777); err != nil {
		return 0, err
	}
	if err := copyAuthFile(config.AuthSource, filepath.Join(config.HostAgentDir, "auth.json")); err != nil {
		return 0, err
	}
	identity := sandboxIdentity{uid: uid, gid: gid}
	if err := chownTree(config.HostAgentDir, identity); err != nil {
		return 0, err
	}
	if err := chownTree(config.ArtifactDir, identity); err != nil {
		return 0, err
	}
	bootstrapHome := "/tmp/pi-eval-bootstrap"
	for name, value := range map[string]string{"HOME": bootstrapHome, "USERPROFILE": bootstrapHome, agentDirEnvironment(): config.HostAgentDir, "PI_EVAL_CONTAINER": "1"} {
		if err := os.Setenv(name, value); err != nil {
			return 0, err
		}
	}
	syscall.Umask(0o022)

	discover, selection, err := ParseEntrypointArgs(args)
	if err != nil {
		return 0, err
	}
	if len(selection.Files) == 0 {
		return 0, errors.New("The container runner requires an explicit eval file.")
	}
	exit := 0
	if discover {
		err = WriteDiscovery(filepath.Join(config.ArtifactDir, "discovered-tests.json"), config.Suites.List(selection))
	} else {
		report := config.Suites.Run(ctx, selection)
		if err = WriteVitestReport(filepath.Join(config.ArtifactDir, "vitest.json"), report); err == nil {
			summary, _ := json.Marshal(map[string]any{"passed": report.NumPassedTests, "failed": report.NumFailedTests, "total": report.NumTotalTests})
			_, _ = fmt.Fprintf(stdout, "%s\n", summary)
		}
		if !report.Success {
			exit = 1
		}
	}
	if err != nil {
		return 0, err
	}
	artifactUID, artifactGID := os.Getenv("PI_EVAL_ARTIFACT_UID"), os.Getenv("PI_EVAL_ARTIFACT_GID")
	if artifactUID != "" && artifactGID != "" {
		hostUID, err := parseEntrypointID("PI_EVAL_ARTIFACT_UID")
		if err != nil {
			return 0, err
		}
		hostGID, err := parseEntrypointID("PI_EVAL_ARTIFACT_GID")
		if err != nil {
			return 0, err
		}
		if err := chownTree(config.ArtifactDir, sandboxIdentity{uid: hostUID, gid: hostGID}); err != nil {
			return 0, err
		}
	}
	return exit, nil
}

// copyAuthFile copies the mounted credential file when it exists (existsSync, then copyFileSync). copyFileSync gives
// the copy the source's mode, so a 0600 credential stays 0600.
func copyAuthFile(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chmod(target, info.Mode().Perm())
}

func compileNamePattern(pattern string) (*regexp.Regexp, error) { return regexp.Compile(pattern) }
