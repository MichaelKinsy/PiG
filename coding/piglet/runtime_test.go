package piglet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type runtimeCommandCall struct {
	name string
	args []string
}

type fakeRuntimeCommands struct {
	outputs map[string][]byte
	errors  map[string]error
	calls   []runtimeCommandCall
	runErr  error
}

func (f *fakeRuntimeCommands) LookPath(name string) (string, error) {
	if err := f.errors["lookpath "+name]; err != nil {
		return "", err
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeRuntimeCommands) Output(_ context.Context, name string, args []string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, runtimeCommandCall{name: name, args: append([]string(nil), args...)})
	if err := f.errors[key]; err != nil {
		return nil, err
	}
	return append([]byte(nil), f.outputs[key]...), nil
}

func (f *fakeRuntimeCommands) Run(_ context.Context, name string, args []string, _ RuntimeIO) error {
	f.calls = append(f.calls, runtimeCommandCall{name: name, args: append([]string(nil), args...)})
	return f.runErr
}

func TestRunAgentEnvironmentImageModeUsesLockedStandardContainer(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(workspace, "dev.piglet.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: dev\nagentEnv:\n  image: example.test/dev@sha256:"+strings.Repeat("a", 64)+"\n  pigRuntime:\n    mode: image\n    version: 0.81.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	commands := &fakeRuntimeCommands{
		outputs: map[string][]byte{
			"docker info": []byte("ready"),
			"docker image inspect --format {{.Id}}\t{{.Os}}\t{{.Architecture}}\t{{.Config.WorkingDir}} example.test/dev@sha256:" + strings.Repeat("a", 64): []byte("sha256:" + strings.Repeat("b", 64) + "\tlinux\tarm64\n"),
			"docker run --rm --pull=never --platform linux/arm64 --entrypoint pig example.test/dev@sha256:" + strings.Repeat("a", 64) + " version":         []byte("pig: 0.81.1\nupstream pi: 0.81.1\n"),
		},
		errors: map[string]error{},
	}
	var stderr bytes.Buffer
	result, err := RunAgentEnvironment(context.Background(), p, AgentEnvironmentRuntimeOptions{
		Engine:     "docker",
		Workspace:  workspace,
		Args:       []string{"--piglet", pigletPath, "--print", "hello"},
		PigVersion: "0.81.1",
		StateRoot:  filepath.Join(root, "state"),
		Commands:   commands,
		IO:         RuntimeIO{Stderr: &stderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Continue || result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(commands.calls) != 4 {
		t.Fatalf("calls = %#v", commands.calls)
	}
	run := strings.Join(commands.calls[3].args, " ")
	for _, required := range []string{
		"run --rm --pull=never --init --platform linux/arm64",
		"--security-opt no-new-privileges --cap-drop ALL",
		"type=bind,src=" + canonicalWorkspace + ",dst=/workspace",
		"dst=/home/pig/.pig",
		"dst=/run/pig/agent-environment.lock.json,readonly",
		"HOME=/home/pig",
		"PIG_AGENT_ENV_ACTIVE=sha256:",
		"PIG_PIGLET_PATH=/run/pig/piglet.yaml",
		"--entrypoint pig example.test/dev@sha256:" + strings.Repeat("a", 64),
		"--print hello",
	} {
		if !strings.Contains(run, required) {
			t.Errorf("run args missing %q:\n%s", required, run)
		}
	}
	if strings.Contains(run, "--piglet "+pigletPath) {
		t.Fatalf("host piglet path leaked into container args: %s", run)
	}
}

func TestRunAgentEnvironmentInnerProcessRequiresMatchingLock(t *testing.T) {
	root := t.TempDir()
	pigletPath := filepath.Join(root, "dev.piglet.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: dev\nagentEnv:\n  image: example.test/dev:1\n  pigRuntime:\n    mode: image\n    version: 0.81.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	commands := &fakeRuntimeCommands{outputs: map[string][]byte{
		"docker info": []byte("ready"),
		"docker image inspect --format {{.Id}}\t{{.Os}}\t{{.Architecture}}\t{{.Config.WorkingDir}} example.test/dev:1": []byte("sha256:" + strings.Repeat("a", 64) + "\tlinux\tamd64\n"),
		"docker run --rm --pull=never --platform linux/amd64 --entrypoint pig example.test/dev:1 version":              []byte("pig: 0.81.1\n"),
	}, errors: map[string]error{}}
	runtimeRoot := filepath.Join(root, "runtime")
	if _, err := RunAgentEnvironment(context.Background(), p, AgentEnvironmentRuntimeOptions{
		Engine: "docker", Workspace: root, StateRoot: runtimeRoot, Commands: commands, IO: RuntimeIO{Stderr: &bytes.Buffer{}},
	}); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(runtimeRoot, "metadata", "agent-environment.lock.json")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document["version"]; exists {
		t.Fatalf("environment lock exposed a Pig-owned format version: %s", data)
	}
	var lock agentEnvironmentLock
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	innerPiglet, err := Parse(filepath.Join(runtimeRoot, "metadata", "piglet.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(agentEnvironmentActiveEnv, lock.Identity)
	t.Setenv(agentEnvironmentLockEnv, lockPath)
	result, err := RunAgentEnvironment(context.Background(), innerPiglet, AgentEnvironmentRuntimeOptions{})
	if err != nil || !result.Continue {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	document["version"] = 1
	former, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, former, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunAgentEnvironment(context.Background(), innerPiglet, AgentEnvironmentRuntimeOptions{}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("former lock version field error = %v", err)
	}
	if err := os.WriteFile(lockPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	lock.ImageID = "sha256:" + strings.Repeat("b", 64)
	tampered, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunAgentEnvironment(context.Background(), innerPiglet, AgentEnvironmentRuntimeOptions{}); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered lock error = %v", err)
	}
}

func TestRunAgentEnvironmentRejectsImageDriftAgainstRecordedLock(t *testing.T) {
	root := t.TempDir()
	pigletPath := filepath.Join(root, "dev.piglet.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: dev\nagentEnv:\n  image: example.test/dev:1\n  pigRuntime:\n    mode: image\n    version: 0.81.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	inspectKey := "docker image inspect --format {{.Id}}\t{{.Os}}\t{{.Architecture}}\t{{.Config.WorkingDir}} example.test/dev:1"
	commands := &fakeRuntimeCommands{outputs: map[string][]byte{
		"docker info": []byte("ready"),
		inspectKey:    []byte("sha256:" + strings.Repeat("a", 64) + "\tlinux\tamd64\n"),
		"docker run --rm --pull=never --platform linux/amd64 --entrypoint pig example.test/dev:1 version": []byte("pig: 0.81.1\n"),
	}, errors: map[string]error{}}
	options := AgentEnvironmentRuntimeOptions{
		Engine: "docker", Workspace: root, Args: []string{"--piglet", pigletPath},
		StateRoot: filepath.Join(root, "runtime"), Commands: commands, IO: RuntimeIO{Stderr: &bytes.Buffer{}},
	}
	if _, err := RunAgentEnvironment(context.Background(), p, options); err != nil {
		t.Fatal(err)
	}
	commands.outputs[inspectKey] = []byte("sha256:" + strings.Repeat("b", 64) + "\tlinux\tamd64\n")
	_, err = RunAgentEnvironment(context.Background(), p, options)
	if err == nil || !strings.Contains(err.Error(), "lock mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunAgentEnvironmentAutoFallsThroughOnlyBeforeContainerStart(t *testing.T) {
	root := t.TempDir()
	pigletPath := filepath.Join(root, "dev.piglet.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: dev\nagentEnv:\n  image: example.test/dev:1\n  pigRuntime:\n    mode: image\n    version: 0.81.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	commands := &fakeRuntimeCommands{
		outputs: map[string][]byte{
			"podman info": []byte("ready"),
			"podman image inspect --format {{.Id}}\t{{.Os}}\t{{.Architecture}}\t{{.Config.WorkingDir}} example.test/dev:1": []byte("sha256:" + strings.Repeat("c", 64) + "\tlinux\tamd64\n"),
			"podman run --rm --pull=never --platform linux/amd64 --entrypoint pig example.test/dev:1 version":              []byte("pig: 0.81.1\n"),
		},
		errors: map[string]error{"docker info": errors.New("daemon unavailable")},
		runErr: errors.New("container failed"),
	}
	_, err = RunAgentEnvironment(context.Background(), p, AgentEnvironmentRuntimeOptions{
		Engine: "auto", Workspace: root, Args: []string{"--piglet", pigletPath}, PigVersion: "0.81.1",
		StateRoot: filepath.Join(root, "state"), Commands: commands, IO: RuntimeIO{Stderr: &bytes.Buffer{}},
	})
	if err == nil || !strings.Contains(err.Error(), "podman failed after execution started") {
		t.Fatalf("error = %v", err)
	}
	for _, call := range commands.calls {
		if call.name == "docker" && len(call.args) > 0 && call.args[0] == "run" {
			t.Fatalf("docker fallback attempted after podman start: %#v", commands.calls)
		}
	}
}

func TestRunAgentEnvironmentRejectsUnsupportedRuntimeSlices(t *testing.T) {
	for _, tc := range []struct {
		name    string
		yaml    string
		message string
	}{
		{name: "inject", yaml: "image: example.test/dev:1", message: "runtime injection is not implemented"},
		{name: "devcontainer", yaml: "devContainer: .devcontainer/devcontainer.json\n  pigRuntime:\n    mode: image", message: "requires Dev Container execution"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.name == "devcontainer" {
				path := filepath.Join(root, ".devcontainer", "devcontainer.json")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`{"image":"example.test/dev:1"}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			pigletPath := filepath.Join(root, "dev.piglet.yaml")
			if err := os.WriteFile(pigletPath, []byte("name: dev\nagentEnv:\n  "+tc.yaml+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := Parse(pigletPath)
			if err != nil {
				t.Fatal(err)
			}
			_, err = RunAgentEnvironment(context.Background(), p, AgentEnvironmentRuntimeOptions{Workspace: root})
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRunAgentEnvironmentUnsafeHostIsVisibleAndOneShot(t *testing.T) {
	p, err := ParseBytes([]byte("name: dev\nagentEnv:\n  image: example.test/dev:1\n"))
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	result, err := RunAgentEnvironment(context.Background(), p, AgentEnvironmentRuntimeOptions{UnsafeHost: true, IO: RuntimeIO{Stderr: &stderr}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Continue || !result.Bypassed || !strings.Contains(stderr.String(), "UNSAFE HOST BYPASS") {
		t.Fatalf("result=%+v stderr=%q", result, stderr.String())
	}
	if p.AgentEnv == nil || p.AgentEnv.Image != "example.test/dev:1" {
		t.Fatalf("Piglet was mutated: %+v", p.AgentEnv)
	}
}

func runImageEnvironment(t *testing.T, agentEnvYAML, workingDir string) []string {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(workspace, "dev.piglet.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: dev\nagentEnv:\n"+agentEnvYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	commands := &fakeRuntimeCommands{outputs: map[string][]byte{
		"docker info": []byte("ready"),
		"docker image inspect --format {{.Id}}\t{{.Os}}\t{{.Architecture}}\t{{.Config.WorkingDir}} example.test/dev:1": []byte("sha256:" + strings.Repeat("a", 64) + "\tlinux\tamd64\t" + workingDir + "\n"),
		"docker run --rm --pull=never --platform linux/amd64 --entrypoint pig example.test/dev:1 version":              []byte("pig: 0.81.1\n"),
	}, errors: map[string]error{}}
	if _, err := RunAgentEnvironment(context.Background(), p, AgentEnvironmentRuntimeOptions{
		Engine: "docker", Workspace: workspace, Args: []string{"--piglet", pigletPath}, StateRoot: filepath.Join(root, "runtime"),
		Commands: commands, IO: RuntimeIO{Stderr: &bytes.Buffer{}},
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, call := range commands.calls {
		if call.name == "docker" && len(call.args) > 0 && call.args[0] == "run" && !contains(call.args, "version") {
			return call.args
		}
	}
	t.Fatalf("no container run recorded: %#v", commands.calls)
	return nil
}

func contains(args []string, want string) bool {
	return slices.Contains(args, want)
}

func TestRunAgentEnvironmentWorkspaceFolderDefaultsToImageWorkingDir(t *testing.T) {
	run := strings.Join(runImageEnvironment(t, "  image: example.test/dev:1\n  pigRuntime:\n    mode: image\n", "/srv/app"), " ")
	if !strings.Contains(run, "--workdir /srv/app") || !strings.Contains(run, ",dst=/srv/app") {
		t.Fatalf("image WorkingDir not honored: %s", run)
	}
	if strings.Contains(run, ",dst=/srv/app,readonly") {
		t.Fatalf("standard workspace should be read-write: %s", run)
	}
}

func TestRunAgentEnvironmentExplicitWorkspaceFolderOverridesImage(t *testing.T) {
	run := strings.Join(runImageEnvironment(t, "  image: example.test/dev:1\n  pigRuntime:\n    mode: image\n  workspace:\n    folder: /code\n", "/srv/app"), " ")
	if !strings.Contains(run, "--workdir /code") || !strings.Contains(run, ",dst=/code") {
		t.Fatalf("explicit folder not honored: %s", run)
	}
}

func TestRunAgentEnvironmentMinimalPresetMakesWorkspaceReadOnly(t *testing.T) {
	run := strings.Join(runImageEnvironment(t, "  image: example.test/dev:1\n  pigRuntime:\n    mode: image\n  policy:\n    preset: minimal\n", ""), " ")
	if !strings.Contains(run, ",dst=/workspace,readonly") {
		t.Fatalf("minimal preset did not make workspace read-only: %s", run)
	}
}

func TestRunAgentEnvironmentElevatedMountsAreExposed(t *testing.T) {
	mountDir := t.TempDir()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(workspace, "dev.piglet.yaml")
	yaml := "name: dev\nagentEnv:\n  image: example.test/dev:1\n  pigRuntime:\n    mode: image\n  policy:\n    preset: elevated\n  mounts:\n    - source: " + mountDir + "\n      target: /cache\n      readonly: true\n"
	if err := os.WriteFile(pigletPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	commands := &fakeRuntimeCommands{outputs: map[string][]byte{
		"docker info": []byte("ready"),
		"docker image inspect --format {{.Id}}\t{{.Os}}\t{{.Architecture}}\t{{.Config.WorkingDir}} example.test/dev:1": []byte("sha256:" + strings.Repeat("a", 64) + "\tlinux\tamd64\t\n"),
		"docker run --rm --pull=never --platform linux/amd64 --entrypoint pig example.test/dev:1 version":              []byte("pig: 0.81.1\n"),
	}, errors: map[string]error{}}
	if _, err := RunAgentEnvironment(context.Background(), p, AgentEnvironmentRuntimeOptions{
		Engine: "docker", Workspace: workspace, Args: []string{"--piglet", pigletPath}, StateRoot: filepath.Join(root, "runtime"),
		Commands: commands, IO: RuntimeIO{Stderr: &bytes.Buffer{}},
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	resolvedMount, _ := filepath.EvalSymlinks(mountDir)
	var run string
	for _, call := range commands.calls {
		if call.name == "docker" && len(call.args) > 0 && call.args[0] == "run" && !contains(call.args, "version") {
			run = strings.Join(call.args, " ")
		}
	}
	if !strings.Contains(run, "type=bind,src="+resolvedMount+",dst=/cache,readonly") {
		t.Fatalf("elevated mount not exposed: %s", run)
	}
}

func TestAgentEnvironmentMountsRequireElevatedAndRejectCollisions(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		message string
	}{
		{name: "standard-rejects-mounts", yaml: "  image: example.test/dev:1\n  mounts:\n    - source: /tmp\n      target: /cache\n", message: "require policy.preset: elevated"},
		{name: "reserved-collision", yaml: "  image: example.test/dev:1\n  policy:\n    preset: elevated\n  mounts:\n    - source: /tmp\n      target: /home/pig/x\n", message: "overlaps the reserved path"},
		{name: "workspace-collision", yaml: "  image: example.test/dev:1\n  policy:\n    preset: elevated\n  workspace:\n    folder: /code\n  mounts:\n    - source: /tmp\n      target: /code/sub\n", message: "overlaps the reserved path"},
		{name: "duplicate-target", yaml: "  image: example.test/dev:1\n  policy:\n    preset: elevated\n  mounts:\n    - source: /tmp\n      target: /a\n    - source: /var\n      target: /a\n", message: "duplicate target"},
		{name: "relative-target", yaml: "  image: example.test/dev:1\n  policy:\n    preset: elevated\n  mounts:\n    - source: /tmp\n      target: cache\n", message: "must be an absolute container path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBytes([]byte("name: dev\nagentEnv:\n" + tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestAgentEnvironmentMountRejectsMissingAndSocketSources(t *testing.T) {
	root := t.TempDir()
	socketPath := filepath.Join(root, "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Skipf("unix socket unavailable: %v", err)
	}
	defer func() { _ = listener.Close() }()
	for _, tc := range []struct {
		name    string
		source  string
		message string
	}{
		{name: "missing", source: filepath.Join(root, "nope"), message: "does not exist"},
		{name: "socket", source: socketPath, message: "socket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pigletPath := filepath.Join(root, tc.name+".piglet.yaml")
			yaml := "name: dev\nagentEnv:\n  image: example.test/dev:1\n  pigRuntime:\n    mode: image\n  policy:\n    preset: elevated\n  mounts:\n    - source: " + tc.source + "\n      target: /cache\n"
			if err := os.WriteFile(pigletPath, []byte(yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := Parse(pigletPath)
			if err != nil {
				t.Fatal(err)
			}
			_, err = RunAgentEnvironment(context.Background(), p, AgentEnvironmentRuntimeOptions{
				Engine: "docker", Workspace: root, Args: []string{"--piglet", pigletPath}, StateRoot: filepath.Join(root, tc.name+"-runtime"),
				Commands: &fakeRuntimeCommands{outputs: map[string][]byte{
					"docker info": []byte("ready"),
					"docker image inspect --format {{.Id}}\t{{.Os}}\t{{.Architecture}}\t{{.Config.WorkingDir}} example.test/dev:1": []byte("sha256:" + strings.Repeat("a", 64) + "\tlinux\tamd64\t\n"),
					"docker run --rm --pull=never --platform linux/amd64 --entrypoint pig example.test/dev:1 version":              []byte("pig: 0.81.1\n"),
				}, errors: map[string]error{}}, IO: RuntimeIO{Stderr: &bytes.Buffer{}},
			})
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestAC7SecretEnvironmentFileIsProtectedAndValuesStayOffArgv(t *testing.T) {
	dir := t.TempDir()
	path, err := writeSecretEnvironmentFile(dir, map[string]string{"TOKEN": "SUPER-SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(path) }()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	requireSecretFileOwnerOnly(t, path, true)
	args, err := runtimeContainerArgs(runtimeContainerPlan{
		workspace: t.TempDir(), stateRoot: t.TempDir(), pigletPath: "piglet.yaml", lockPath: "lock.json",
		envFilePath: path, image: "dev:1", lock: agentEnvironmentLock{Platform: "linux/amd64", WorkspaceFolder: "/workspace"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--env-file "+path) || strings.Contains(joined, "SUPER-SECRET") {
		t.Fatalf("argv = %q", joined)
	}
}

func TestAC7UnsafeHostDoesNotBypassMissingSecret(t *testing.T) {
	p := &Piglet{
		Name:    "secret-env",
		Secrets: []SecretDeclaration{{Name: "token", From: SecretSource{Env: "MISSING_SECRET"}}},
		AgentEnv: &AgentEnvironment{
			Image: "dev:1", Secrets: []AgentSecretBinding{{SecretRef: "token", Target: AgentSecretTarget{Env: "TOKEN"}}},
		},
	}
	_, err := RunAgentEnvironment(context.Background(), p, AgentEnvironmentRuntimeOptions{UnsafeHost: true, IO: RuntimeIO{Stderr: io.Discard}})
	if err == nil || !strings.Contains(err.Error(), "missing or empty") {
		t.Fatalf("error = %v", err)
	}
}

func TestAgentEnvironmentExtendsFailsBeforeLaunchUntilClosureStaging(t *testing.T) {
	p := &Piglet{
		Name: "derived-env", AgentEnv: &AgentEnvironment{Image: "dev:1"},
		lineage: []LineageEntry{{Source: "local:base.yaml", Digest: "sha256:" + strings.Repeat("a", 64)}, {Source: "local:child.yaml", Digest: "sha256:" + strings.Repeat("b", 64)}},
	}
	commands := &fakeRuntimeCommands{}
	_, err := RunAgentEnvironment(context.Background(), p, AgentEnvironmentRuntimeOptions{Commands: commands, IO: RuntimeIO{Stderr: io.Discard}})
	if err == nil || !strings.Contains(err.Error(), "effective dependency closure") {
		t.Fatalf("error = %v", err)
	}
	if len(commands.calls) != 0 {
		t.Fatalf("container commands ran before closure failure: %#v", commands.calls)
	}
}
