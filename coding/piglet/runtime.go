package piglet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

const (
	agentEnvironmentActiveEnv = "PIG_AGENT_ENV_ACTIVE"
	agentEnvironmentLockEnv   = "PIG_AGENT_ENV_LOCK"
)

// RuntimeIO supplies the streams inherited by the container engine.
type RuntimeIO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	TTY    bool
}

// AgentEnvironmentRuntimeOptions are machine-local execution inputs. They are
// never serialized into a Piglet or included in its portable identity.
type AgentEnvironmentRuntimeOptions struct {
	Engine     string
	Workspace  string
	Args       []string
	PigVersion string
	StateRoot  string
	UnsafeHost bool
	Commands   runtimeCommands
	IO         RuntimeIO
}

// AgentEnvironmentRuntimeResult tells the caller whether startup should
// continue in the current process or terminate with the child engine's status.
type AgentEnvironmentRuntimeResult struct {
	Continue bool
	Bypassed bool
	ExitCode int
}

type runtimeCommands interface {
	LookPath(name string) (string, error)
	Output(ctx context.Context, name string, args []string) ([]byte, error)
	Run(ctx context.Context, name string, args []string, streams RuntimeIO) error
}

type osRuntimeCommands struct{}

func (osRuntimeCommands) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (osRuntimeCommands) Output(ctx context.Context, name string, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func (osRuntimeCommands) Run(ctx context.Context, name string, args []string, streams RuntimeIO) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = streams.Stdin
	command.Stdout = streams.Stdout
	command.Stderr = streams.Stderr
	return command.Run()
}

type agentEnvironmentLock struct {
	Identity          string             `json:"identity"`
	PigletDigest      string             `json:"pigletDigest"`
	ImageReference    string             `json:"imageReference"`
	ImageID           string             `json:"imageId"`
	Platform          string             `json:"platform"`
	RuntimeMode       string             `json:"runtimeMode"`
	RuntimeRequested  string             `json:"runtimeRequested"`
	RuntimeVersion    string             `json:"runtimeVersion"`
	PolicyPreset      string             `json:"policyPreset"`
	WorkspaceFolder   string             `json:"workspaceFolder"`
	WorkspaceReadOnly bool               `json:"workspaceReadonly"`
	Mounts            []agentMountRecord `json:"mounts,omitempty"`
}

type agentMountRecord struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readonly"`
}

type inspectedRuntimeImage struct {
	ID         string
	Platform   string
	WorkingDir string
}

// RunAgentEnvironment enters a supported required Piglet environment before
// model, session, extension, or tool initialization. The current slice supports
// direct-image Piglets in explicit image-runtime mode with standard policy.
// Every other environment form remains fail-closed.
func RunAgentEnvironment(ctx context.Context, p *Piglet, options AgentEnvironmentRuntimeOptions) (AgentEnvironmentRuntimeResult, error) {
	if p == nil || p.AgentEnv == nil {
		return AgentEnvironmentRuntimeResult{Continue: true}, nil
	}
	if options.IO.Stderr == nil {
		options.IO.Stderr = io.Discard
	}
	if activeIdentity := strings.TrimSpace(os.Getenv(agentEnvironmentActiveEnv)); activeIdentity != "" {
		if err := verifyActiveAgentEnvironment(p, activeIdentity); err != nil {
			return AgentEnvironmentRuntimeResult{}, err
		}
		return AgentEnvironmentRuntimeResult{Continue: true}, nil
	}
	if lineage, _, _ := p.ResolutionIdentity(); len(lineage) > 1 {
		return AgentEnvironmentRuntimeResult{}, fmt.Errorf("Piglet %q uses extends with agentEnv; staging the effective dependency closure into the environment is not implemented", p.Name)
	}
	resolvedSecrets, err := ResolveRequiredSecrets(ctx, p)
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	defer resolvedSecrets.Clear()
	secretEnvironment, err := resolvedSecrets.AgentEnvironment(p)
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	runtimeSecretEnvironment, err := resolvedSecrets.RuntimeEnvironment()
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	maps.Copy(secretEnvironment, runtimeSecretEnvironment)
	if options.UnsafeHost {
		_, _ = fmt.Fprintf(options.IO.Stderr, "WARNING: UNSAFE HOST BYPASS for Piglet %q: required agentEnv is not active; environment-only mounts, state, and configuration are unavailable.\n", p.Name)
		return AgentEnvironmentRuntimeResult{Continue: true, Bypassed: true}, nil
	}
	if activeIdentity := strings.TrimSpace(os.Getenv(agentEnvironmentActiveEnv)); activeIdentity != "" {
		if err := verifyActiveAgentEnvironment(p, activeIdentity); err != nil {
			return AgentEnvironmentRuntimeResult{}, err
		}
		return AgentEnvironmentRuntimeResult{Continue: true}, nil
	}

	if err := p.ValidateAgentEnvironmentLaunch(); err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	effective := p.EffectiveAgentEnvironment()
	resolved, err := ResolveAgentEnvironment(p)
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	if resolved.Form != "image" {
		return AgentEnvironmentRuntimeResult{}, fmt.Errorf("Piglet %q agentEnv %s resolved unexpectedly for direct-image execution", p.Name, resolved.Form)
	}
	if p.SourcePath() == "" {
		return AgentEnvironmentRuntimeResult{}, fmt.Errorf("Piglet %q agentEnv execution requires a source Piglet path; baked Piglet environment execution is not implemented", p.Name)
	}

	workspace, err := runtimeWorkspace(options.Workspace)
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	commands := options.Commands
	if commands == nil {
		commands = osRuntimeCommands{}
	}
	engine, image, err := selectRuntimeEngine(ctx, commands, options.Engine, resolved.Value)
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	if image.Platform == "" || !strings.HasPrefix(image.Platform, "linux/") {
		return AgentEnvironmentRuntimeResult{}, fmt.Errorf("agent environment image %q has unsupported platform %q; direct-image execution requires linux/<arch>", resolved.Value, image.Platform)
	}

	requestedVersion := strings.TrimSpace(effective.PigRuntime.Version)
	resolvedVersion, err := verifyImagePig(ctx, commands, engine, resolved.Value, image.Platform, requestedVersion)
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, fmt.Errorf("%s failed after execution started; no fallback was attempted: %w", engine, err)
	}

	workspaceFolder := resolveWorkspaceFolder(effective, image.WorkingDir)
	workspaceReadOnly := resolveWorkspaceReadOnly(effective)
	mounts, err := resolveAgentMounts(effective)
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}

	pigletDigest, err := digestFile(p.SourcePath())
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, fmt.Errorf("digest Piglet: %w", err)
	}
	lock := agentEnvironmentLock{
		PigletDigest: pigletDigest, ImageReference: resolved.Value,
		ImageID: image.ID, Platform: image.Platform, RuntimeMode: effective.PigRuntime.Mode,
		RuntimeRequested: requestedVersion, RuntimeVersion: resolvedVersion, PolicyPreset: effective.Policy.Preset,
		WorkspaceFolder: workspaceFolder, WorkspaceReadOnly: workspaceReadOnly, Mounts: mounts,
	}
	lock.Identity, err = agentEnvironmentIdentity(lock)
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	runtimeRoot := options.StateRoot
	if runtimeRoot == "" {
		runtimeRoot = filepath.Join(codingagent.StateDir("piglet-runtime"), "environments", strings.TrimPrefix(pigletDigest, "sha256:"))
	}
	runtimeRoot, err = filepath.Abs(runtimeRoot)
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, fmt.Errorf("resolve agent environment runtime root: %w", err)
	}
	stateRoot := filepath.Join(runtimeRoot, "state")
	metadataRoot := filepath.Join(runtimeRoot, "metadata")
	for _, path := range []string{stateRoot, metadataRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return AgentEnvironmentRuntimeResult{}, fmt.Errorf("create agent environment runtime directory: %w", err)
		}
	}
	stagedPigletPath := filepath.Join(metadataRoot, "piglet.yaml")
	if err := copyRuntimePiglet(p.SourcePath(), stagedPigletPath); err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	lockPath := filepath.Join(metadataRoot, "agent-environment.lock.json")
	if err := requireAgentEnvironmentLockAgreement(lockPath, lock); err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	if err := writeAgentEnvironmentLock(lockPath, lock); err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	secretEnvPath := ""
	if len(secretEnvironment) > 0 {
		secretEnvPath, err = writeSecretEnvironmentFile(metadataRoot, secretEnvironment)
		if err != nil {
			return AgentEnvironmentRuntimeResult{}, err
		}
		defer func() { _ = os.Remove(secretEnvPath) }()
	}

	args, err := runtimeContainerArgs(runtimeContainerPlan{
		originalArgs: options.Args,
		workspace:    workspace,
		stateRoot:    stateRoot,
		pigletPath:   stagedPigletPath,
		lockPath:     lockPath,
		envFilePath:  secretEnvPath,
		lock:         lock,
		image:        resolved.Value,
		tty:          options.IO.TTY,
	})
	if err != nil {
		return AgentEnvironmentRuntimeResult{}, err
	}
	if err := commands.Run(ctx, engine, args, options.IO); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return AgentEnvironmentRuntimeResult{ExitCode: exitErr.ExitCode()}, nil
		}
		return AgentEnvironmentRuntimeResult{}, fmt.Errorf("%s failed after execution started; no fallback was attempted: %w", engine, err)
	}
	return AgentEnvironmentRuntimeResult{ExitCode: 0}, nil
}

func writeSecretEnvironmentFile(dir string, environment map[string]string) (string, error) {
	file, err := ownerfile.CreateTemp(dir, ".secret-environment-*")
	if err != nil {
		return "", fmt.Errorf("stage secret environment: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	for _, name := range slices.Sorted(maps.Keys(environment)) {
		if _, err := fmt.Fprintf(file, "%s=%s\n", name, environment[name]); err != nil {
			cleanup()
			return "", fmt.Errorf("write secret environment: %w", err)
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func selectRuntimeEngine(ctx context.Context, commands runtimeCommands, requested, imageReference string) (string, inspectedRuntimeImage, error) {
	if requested == "" {
		requested = "auto"
	}
	if requested != "auto" && requested != "docker" && requested != "podman" {
		return "", inspectedRuntimeImage{}, fmt.Errorf("container engine must be auto, docker, or podman, got %q", requested)
	}
	engines := []string{requested}
	if requested == "auto" {
		engines = []string{"docker", "podman"}
	}
	var unavailable []string
	for _, engine := range engines {
		if _, err := commands.LookPath(engine); err != nil {
			unavailable = append(unavailable, engine+": executable not found")
			continue
		}
		if _, err := commands.Output(ctx, engine, []string{"info"}); err != nil {
			unavailable = append(unavailable, engine+": "+err.Error())
			continue
		}
		image, err := inspectRuntimeImage(ctx, commands, engine, imageReference)
		if err != nil {
			unavailable = append(unavailable, engine+": "+err.Error())
			continue
		}
		return engine, image, nil
	}
	return "", inspectedRuntimeImage{}, fmt.Errorf("agent environment is unavailable (%s); load image %q explicitly with Docker or Podman; Pig never pulls or logs in automatically", strings.Join(unavailable, "; "), imageReference)
}

func inspectRuntimeImage(ctx context.Context, commands runtimeCommands, engine, imageReference string) (inspectedRuntimeImage, error) {
	output, err := commands.Output(ctx, engine, []string{"image", "inspect", "--format", "{{.Id}}\t{{.Os}}\t{{.Architecture}}\t{{.Config.WorkingDir}}", imageReference})
	if err != nil {
		return inspectedRuntimeImage{}, fmt.Errorf("local image inspect failed: %w", err)
	}
	line, _, _ := strings.Cut(string(output), "\n")
	parts := strings.Split(line, "\t")
	if len(parts) < 3 || !strings.HasPrefix(parts[0], "sha256:") || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return inspectedRuntimeImage{}, fmt.Errorf("local image inspect returned malformed identity %q", strings.TrimSpace(line))
	}
	workingDir := ""
	if len(parts) >= 4 {
		workingDir = strings.TrimSpace(parts[3])
	}
	return inspectedRuntimeImage{ID: strings.TrimSpace(parts[0]), Platform: strings.TrimSpace(parts[1]) + "/" + strings.TrimSpace(parts[2]), WorkingDir: workingDir}, nil
}

// resolveWorkspaceFolder picks the container path the invocation workspace binds
// to: an explicit Piglet folder, else the image WorkingDir, else /workspace.
func resolveWorkspaceFolder(effective *AgentEnvironment, imageWorkingDir string) string {
	if effective.Workspace != nil && effective.Workspace.Folder != "" {
		return cleanContainerPath(effective.Workspace.Folder)
	}
	if dir := strings.TrimSpace(imageWorkingDir); dir != "" && dir != "/" && strings.HasPrefix(dir, "/") {
		return cleanContainerPath(dir)
	}
	return "/workspace"
}

func resolveWorkspaceReadOnly(effective *AgentEnvironment) bool {
	if effective.Workspace != nil && effective.Workspace.ReadOnly != nil {
		return *effective.Workspace.ReadOnly
	}
	return effective.Policy.Preset == "minimal"
}

// resolveAgentMounts validates and resolves elevated Piglet bind mounts to
// concrete host sources. Sources must already exist; Pig never creates them.
func resolveAgentMounts(effective *AgentEnvironment) ([]agentMountRecord, error) {
	if len(effective.Mounts) == 0 {
		return nil, nil
	}
	records := make([]agentMountRecord, 0, len(effective.Mounts))
	for i, mount := range effective.Mounts {
		source, err := resolveMountSource(mount.Source)
		if err != nil {
			return nil, fmt.Errorf("agentEnv.mounts[%d].source %q: %w", i, mount.Source, err)
		}
		records = append(records, agentMountRecord{Source: source, Target: cleanContainerPath(mount.Target), ReadOnly: mount.ReadOnly})
	}
	return records, nil
}

func resolveMountSource(raw string) (string, error) {
	path := expandOriginPath("", raw)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("mount source must resolve to an absolute host path")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("mount source does not exist")
	}
	if info.Mode()&os.ModeSocket != 0 {
		return "", fmt.Errorf("mount source is a socket; container-engine sockets must never be mounted")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve mount source symlinks: %w", err)
	}
	resolved = filepath.Clean(resolved)
	if resolved == string(filepath.Separator) {
		return "", fmt.Errorf("mounting the host root filesystem is forbidden")
	}
	if slices.Contains([]string{"/var/run/docker.sock", "/run/docker.sock", "/run/podman/podman.sock"}, resolved) {
		return "", fmt.Errorf("mounting the container-engine socket is forbidden")
	}
	return resolved, nil
}

func validateImageRuntimePigletClosure(p *Piglet) error {
	if len(p.Packages) > 0 {
		return fmt.Errorf("Piglet %q image runtime cannot localize Package dependencies yet; host fallback is forbidden", p.Name)
	}
	for _, extension := range p.Extensions {
		if len(extension.Origins) > 0 {
			return fmt.Errorf("Piglet %q image runtime cannot localize extension %q yet; host fallback is forbidden", p.Name, extension.Name)
		}
	}
	for _, skill := range p.Skills {
		if skill.Content == "" && len(skill.Origins) > 0 {
			return fmt.Errorf("Piglet %q image runtime cannot localize skill %q yet; host fallback is forbidden", p.Name, skill.Name)
		}
	}
	if p.SystemPrompt != nil && p.SystemPrompt.File != "" {
		return fmt.Errorf("Piglet %q image runtime requires an inline system prompt", p.Name)
	}
	return nil
}

func copyRuntimePiglet(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read runtime Piglet: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".piglet-*")
	if err != nil {
		return fmt.Errorf("stage runtime Piglet: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("commit runtime Piglet: %w", err)
	}
	return nil
}

func verifyImagePig(ctx context.Context, commands runtimeCommands, engine, imageReference, platform, expectedVersion string) (string, error) {
	output, err := commands.Output(ctx, engine, []string{"run", "--rm", "--pull=never", "--platform", platform, "--entrypoint", "pig", imageReference, "version"})
	if err != nil {
		return "", fmt.Errorf("image Pig compatibility check: %w", err)
	}
	version := ""
	for line := range strings.SplitSeq(string(output), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "pig: "); ok {
			version = strings.TrimSpace(value)
			break
		}
	}
	if version == "" {
		return "", fmt.Errorf("image Pig compatibility check returned malformed version output %q", strings.TrimSpace(string(output)))
	}
	if expectedVersion != "" && expectedVersion != "latest" && version != expectedVersion {
		return "", fmt.Errorf("image Pig version mismatch: expected %s, got %s", expectedVersion, version)
	}
	return version, nil
}

type runtimeContainerPlan struct {
	originalArgs []string
	workspace    string
	stateRoot    string
	pigletPath   string
	lockPath     string
	envFilePath  string
	lock         agentEnvironmentLock
	image        string
	tty          bool
}

func runtimeContainerArgs(plan runtimeContainerPlan) ([]string, error) {
	uid, gid := runtimeUserIDs()
	workspaceMount := "type=bind,src=" + plan.workspace + ",dst=" + plan.lock.WorkspaceFolder
	if plan.lock.WorkspaceReadOnly {
		workspaceMount += ",readonly"
	}
	args := []string{
		"run", "--rm", "--pull=never", "--init", "--platform", plan.lock.Platform,
		"--user", uid + ":" + gid,
		"--security-opt", "no-new-privileges", "--cap-drop", "ALL",
		"--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=1g",
		"--memory", "8g", "--cpus", "4", "--pids-limit", "512",
		"--network", "bridge",
		"--workdir", plan.lock.WorkspaceFolder,
		"--mount", workspaceMount,
		"--mount", "type=bind,src=" + plan.stateRoot + ",dst=/home/pig/.pig",
		"--mount", "type=bind,src=" + plan.pigletPath + ",dst=/run/pig/piglet.yaml,readonly",
		"--mount", "type=bind,src=" + plan.lockPath + ",dst=/run/pig/agent-environment.lock.json,readonly",
	}
	if plan.envFilePath != "" {
		args = append(args, "--env-file", plan.envFilePath)
	}
	for _, mount := range plan.lock.Mounts {
		spec := "type=bind,src=" + mount.Source + ",dst=" + mount.Target
		if mount.ReadOnly {
			spec += ",readonly"
		}
		args = append(args, "--mount", spec)
	}
	args = append(args,
		"--env", "HOME=/home/pig", "--env", "PIG_HOME=/home/pig/.pig",
		"--env", "PIG_CODING_AGENT_DIR=/home/pig/.pig/agent",
		"--env", agentEnvironmentActiveEnv+"="+plan.lock.Identity,
		"--env", agentEnvironmentLockEnv+"=/run/pig/agent-environment.lock.json",
		"--env", "PIG_PIGLET_PATH=/run/pig/piglet.yaml",
	)
	if plan.tty {
		args = append(args, "-i", "-t")
	}
	args = append(args, "--entrypoint", "pig", plan.image)
	args = append(args, stripRuntimeFlags(plan.originalArgs)...)
	return args, nil
}

func stripRuntimeFlags(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--unsafe-host":
			continue
		case arg == "--piglet" || arg == "--container-engine" || arg == "--workspace":
			if i+1 < len(args) {
				i++
			}
			continue
		case strings.HasPrefix(arg, "--piglet=") || strings.HasPrefix(arg, "--container-engine=") || strings.HasPrefix(arg, "--workspace="):
			continue
		default:
			out = append(out, arg)
		}
	}
	return out
}

// ActiveAgentEnvironmentIdentity returns the already-verified active
// environment identity injected into the inner process, or empty for host
// execution. It never reads secret values.
func ActiveAgentEnvironmentIdentity() string {
	return strings.TrimSpace(os.Getenv(agentEnvironmentActiveEnv))
}

func verifyActiveAgentEnvironment(p *Piglet, activeIdentity string) error {
	lockPath := strings.TrimSpace(os.Getenv(agentEnvironmentLockEnv))
	if lockPath == "" {
		return fmt.Errorf("Piglet %q agent environment marker is set without a lock file; refusing host execution", p.Name)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return fmt.Errorf("read active agent environment lock: %w", err)
	}
	var lock agentEnvironmentLock
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lock); err != nil {
		return fmt.Errorf("parse active agent environment lock: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("parse active agent environment lock: trailing content")
	}
	if lock.Identity != activeIdentity {
		return fmt.Errorf("active agent environment lock identity mismatch")
	}
	pigletDigest, err := digestFile(p.SourcePath())
	if err != nil {
		return fmt.Errorf("digest active Piglet: %w", err)
	}
	effective := p.EffectiveAgentEnvironment()
	if lock.PigletDigest != pigletDigest || lock.ImageReference != effective.Image || lock.RuntimeMode != effective.PigRuntime.Mode || lock.RuntimeRequested != effective.PigRuntime.Version || lock.PolicyPreset != effective.Policy.Preset {
		return fmt.Errorf("active agent environment lock does not match Piglet %q", p.Name)
	}
	identity, err := agentEnvironmentIdentity(lock)
	if err != nil {
		return err
	}
	if identity != activeIdentity {
		return fmt.Errorf("active agent environment lock digest mismatch")
	}
	return nil
}

func requireAgentEnvironmentLockAgreement(path string, current agentEnvironmentLock) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read recorded agent environment lock: %w", err)
	}
	var recorded agentEnvironmentLock
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&recorded); err != nil {
		return fmt.Errorf("parse recorded agent environment lock: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("parse recorded agent environment lock: trailing content")
	}
	identity, err := agentEnvironmentIdentity(recorded)
	if err != nil {
		return err
	}
	if recorded.Identity != identity {
		return fmt.Errorf("recorded agent environment lock is invalid")
	}
	if recorded.Identity != current.Identity {
		return fmt.Errorf("agent environment lock mismatch: image or runtime identity changed for the same Piglet; change the Piglet image reference explicitly before launching")
	}
	return nil
}

func writeAgentEnvironmentLock(path string, lock agentEnvironmentLock) error {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".agent-environment-lock-*")
	if err != nil {
		return fmt.Errorf("stage agent environment lock: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit agent environment lock: %w", err)
	}
	return nil
}

func agentEnvironmentIdentity(lock agentEnvironmentLock) (string, error) {
	copy := lock
	copy.Identity = ""
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func digestFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func runtimeWorkspace(path string) (string, error) {
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve workspace: %w", err)
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("agent environment workspace: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("agent environment workspace %s is not a directory", absolute)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve agent environment workspace symlinks: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func runtimeUserIDs() (string, string) {
	current, err := user.Current()
	if err == nil {
		if _, uidErr := strconv.ParseUint(current.Uid, 10, 32); uidErr == nil {
			if _, gidErr := strconv.ParseUint(current.Gid, 10, 32); gidErr == nil {
				return current.Uid, current.Gid
			}
		}
	}
	if runtime.GOOS == "windows" {
		return "65532", "65532"
	}
	return "65532", "65532"
}
