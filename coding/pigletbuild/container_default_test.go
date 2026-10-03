package pigletbuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
)

func lookPathTable(found ...string) executableLookup {
	return func(name string) (string, error) {
		if slices.Contains(found, name) {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New(name + ": not found")
	}
}

func testDefaultContainer(release string, lookPath executableLookup, run containerCommandRunner, cacheDir string) containerBuilder {
	builder := defaultContainerBuilder()
	builder.lookPath = lookPath
	builder.run = run
	builder.bootstrap.runningRelease = func() (string, bool) { return release, release != "" }
	if cacheDir != "" {
		builder.bootstrap.cacheDir = cacheDir
	}
	return builder
}

func TestDefaultContainerImageIsTheCIGoImageBase(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "automation", "images", "ci-go", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\nARG GO_IMAGE="+defaultContainerImage+"\n") {
		t.Fatalf("default container image %s is not the ci-go GO_IMAGE pin", defaultContainerImage)
	}
	if !containerImagePattern.MatchString(defaultContainerImage) {
		t.Fatalf("default container image %s is not digest-pinned", defaultContainerImage)
	}
}

func TestDefaultContainerEngineSelection(t *testing.T) {
	cases := []struct {
		name   string
		env    string
		found  []string
		want   string
		errHas string
	}{
		{name: "prefers podman", found: []string{"docker", "podman"}, want: "podman"},
		{name: "docker only", found: []string{"docker"}, want: "docker"},
		{name: "env selects docker", env: "docker", found: []string{"docker", "podman"}, want: "docker"},
		{name: "env engine missing", env: "podman", found: []string{"docker"}, errHas: "PIG_CONTAINER_ENGINE=podman"},
		{name: "env engine invalid", env: "nerdctl", found: []string{"docker", "podman"}, errHas: "must be docker or podman"},
		{name: "none", errHas: "neither podman nor docker"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(containerEngineEnv, test.env)
			got, err := resolveDefaultEngine(lookPathTable(test.found...))
			if test.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), test.errHas) {
					t.Fatalf("engine=%q err=%v, want error containing %q", got, err, test.errHas)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("engine=%q err=%v, want %q", got, err, test.want)
			}
		})
	}
}

func TestDefaultContainerProbeIsReadyWithoutPulling(t *testing.T) {
	t.Setenv(containerEngineEnv, "")
	var calls [][]string
	var engines []string
	run := func(_ context.Context, engine string, args ...string) ([]byte, error) {
		engines = append(engines, engine)
		calls = append(calls, append([]string(nil), args...))
		return []byte("ok"), nil
	}
	builder := testDefaultContainer("v9.8.7", lookPathTable("docker", "podman"), run, "")
	readiness := builder.Probe(context.Background(), BuilderRequest{Options: Options{Targets: []Target{{OS: "linux", Arch: "arm64"}}}})
	if !readiness.Ready || readiness.Builder != "container" {
		t.Fatalf("readiness = %+v", readiness)
	}
	if len(calls) != 1 || calls[0][0] != "info" || engines[0] != "podman" {
		t.Fatalf("probe calls = %v on %v", calls, engines)
	}
}

func TestDefaultContainerProbeNotReady(t *testing.T) {
	t.Setenv(containerEngineEnv, "")
	ok := func(context.Context, string, ...string) ([]byte, error) { return []byte("ok"), nil }
	linux := []Target{{OS: "linux", Arch: "amd64"}}
	cases := []struct {
		name    string
		release string
		found   []string
		run     containerCommandRunner
		options Options
		code    string
	}{
		{name: "dev build", release: "", found: []string{"podman"}, run: ok, options: Options{Targets: linux}, code: "source-unavailable"},
		{name: "non-linux target", release: "v9.8.7", found: []string{"podman"}, run: ok, options: Options{Targets: []Target{{OS: "darwin", Arch: "arm64"}}}, code: "target-unavailable"},
		{name: "signing", release: "v9.8.7", found: []string{"podman"}, run: ok, options: Options{Targets: linux, SignKeyPath: "key"}, code: "signing-unsupported"},
		{name: "no engine", release: "v9.8.7", run: ok, options: Options{Targets: linux}, code: "engine-unavailable"},
		{name: "engine stopped", release: "v9.8.7", found: []string{"docker"}, run: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("cannot connect to the Docker daemon")
		}, options: Options{Targets: linux}, code: "engine-unavailable"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := testDefaultContainer(test.release, lookPathTable(test.found...), test.run, "")
			readiness := builder.Probe(context.Background(), BuilderRequest{Options: test.options})
			if readiness.Ready || readiness.Code != test.code || readiness.Remedy == "" {
				t.Fatalf("readiness = %+v, want code %s", readiness, test.code)
			}
		})
	}
}

func TestSelectBuilderAutoUsesContainerWhenNativeIsNotReady(t *testing.T) {
	t.Setenv(containerEngineEnv, "")
	other := "arm64"
	if runtime.GOARCH == "arm64" {
		other = "amd64"
	}
	request := BuilderRequest{Options: Options{Targets: []Target{{OS: "linux", Arch: other}}}}
	ok := func(context.Context, string, ...string) ([]byte, error) { return []byte("ok"), nil }
	builders := []BuilderBackend{nativeBuilder{}, testDefaultContainer("v9.8.7", lookPathTable("docker"), ok, "")}
	selected, readiness, err := selectBuilder(context.Background(), builders, "auto", request)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Name() != "container" || !readiness.Ready {
		t.Fatalf("selected=%s readiness=%+v", selected.Name(), readiness)
	}
	if _, _, err := selectBuilder(context.Background(), builders, "container", BuilderRequest{Options: Options{Targets: []Target{{OS: "linux", Arch: runtime.GOARCH}}}}); err != nil {
		t.Fatalf("--builder container: %v", err)
	}
}

func TestDefaultContainerBuildInstallsRunningRelease(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv(containerEngineEnv, "")
	pigletPath := filepath.Join(t.TempDir(), "release.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{OS: "linux", Arch: "arm64"}
	opts := Options{Targets: []Target{target}, Sandbox: Sandbox{Native: target}, Version: "1.0.0", BakedSettings: []byte("name: release\n")}
	output := filepath.Join(t.TempDir(), "pig-release")
	cacheDir := filepath.Join(t.TempDir(), "container-go")
	var runArgs []string
	var runEngine string
	run := func(_ context.Context, engine string, args ...string) ([]byte, error) {
		runEngine = engine
		runArgs = append([]string(nil), args...)
		return nil, writeFakeContainerOutputs(args, output, opts, target)
	}
	builder := testDefaultContainer("v9.8.7", lookPathTable("docker", "podman"), run, cacheDir)
	result, err := builder.Build(context.Background(), BuilderRequest{Piglet: parsed, Options: opts, Output: output, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if result.Builder != "container" || result.Artifact != output || runEngine != "podman" {
		t.Fatalf("result=%+v engine=%s", result, runEngine)
	}
	if info, err := os.Stat(cacheDir); err != nil || !info.IsDir() {
		t.Fatalf("container Go cache %s not created: %v", cacheDir, err)
	}
	joined := strings.Join(runArgs, " ")
	for _, required := range []string{
		"run --rm --pull=missing --platform linux/arm64",
		"src=" + cacheDir + ",dst=/pig-go-cache",
		"GOMODCACHE=/pig-go-cache/mod",
		"--entrypoint sh " + defaultContainerImage + " -c",
		"go install github.com/MichaelKinsy/PiG/cmd/pig@\"$0\"",
		"v9.8.7 piglet build /input/piglet.yaml --format binary --builder native",
		"--targets linux/arm64",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("run args missing %q: %s", required, joined)
		}
	}
	recordData, err := os.ReadFile(result.Record)
	if err != nil {
		t.Fatal(err)
	}
	record, err := pigletartifact.ParseRecord(recordData)
	if err != nil {
		t.Fatal(err)
	}
	if record.Binary.Builder != "container" || record.Binary.BuilderIdentity != "container:podman:"+defaultContainerImage {
		t.Fatalf("record builder=%q identity=%q", record.Binary.Builder, record.Binary.BuilderIdentity)
	}
}

// writeFakeContainerOutputs plays the inner native build: it writes the artifact and its records
// into the mounted output and record directories.
func writeFakeContainerOutputs(args []string, output string, opts Options, target Target) error {
	outputDir := hostMountSource(args, "/out")
	recordHome := hostMountSource(args, "/records-home")
	if outputDir == "" || recordHome == "" {
		return fmt.Errorf("missing output or record mount: %v", args)
	}
	artifactData := []byte("container-artifact")
	if err := os.WriteFile(filepath.Join(outputDir, filepath.Base(output)), artifactData, 0o755); err != nil {
		return err
	}
	componentPlan, err := pigletartifact.BuildPlan(nil)
	if err != nil {
		return err
	}
	resolution, err := pigletartifact.NewResolutionRecord("release", opts.Version, time.Unix(1, 0), pigletartifact.ResolutionInput{
		SourceDigest: "sha256:" + strings.Repeat("b", 64), EffectiveDigest: digestBytes(opts.BakedSettings), ComponentPlan: componentPlan,
	})
	if err != nil {
		return err
	}
	binary, err := pigletartifact.NewBinaryRecord("release", opts.Version, time.Unix(2, 0), resolution, pigletartifact.BinaryInput{
		Target: target.String(), PigVersion: "test", PigSourceRevision: "revision", PigSourceDigest: "sha256:" + strings.Repeat("c", 64),
		Builder: "native", BuilderIdentity: "native:revision", Toolchains: map[string]string{"go": "go version test"},
		Artifact:     pigletartifact.Artifact{Digest: digestBytes(artifactData), Size: int64(len(artifactData)), FileName: "pig-release"},
		Verification: pigletartifact.Verification{Policy: "basic", Passed: true, Checks: []string{"artifact-sha256", "artifact-version-smoke"}},
	})
	if err != nil {
		return err
	}
	for name, record := range map[string]pigletartifact.Record{"resolution.json": resolution, "binary.json": binary} {
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		path := filepath.Join(recordHome, "receipts", "piglets", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// TestCreateContainerMountpointsUnderReadOnlyInput guards the engine failure "make mountpoint
// …: read-only file system": nested input mounts need their mountpoints in the host input dir.
func TestCreateContainerMountpointsUnderReadOnlyInput(t *testing.T) {
	sourceDir := t.TempDir()
	sourceFile := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(sourceFile, []byte("prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	inputDir := t.TempDir()
	mounts := []containerPigletMount{
		{Source: sourceDir, Target: "/input/sources/extensions/hello"},
		{Source: sourceFile, Target: "/input/sources/prompts/prompt.md"},
	}
	if err := createContainerMountpoints(inputDir, mounts); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(inputDir, "sources", "extensions", "hello")); err != nil || !info.IsDir() {
		t.Fatalf("directory mountpoint: %v", err)
	}
	if info, err := os.Stat(filepath.Join(inputDir, "sources", "prompts", "prompt.md")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("file mountpoint: %v", err)
	}
	if err := createContainerMountpoints(inputDir, []containerPigletMount{{Source: sourceDir, Target: "/etc/x"}}); err == nil {
		t.Fatal("mount outside /input accepted")
	}
}

// TestDefaultContainerBuildCreatesNestedInputMountpoints guards the Build call site of
// createContainerMountpoints. A local extension is bind-mounted below the read-only /input mount,
// so its mountpoint must already exist in the host input directory when the engine runs.
func TestDefaultContainerBuildCreatesNestedInputMountpoints(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv(containerEngineEnv, "")
	parsed, err := piglet.Parse(writeSmallPiglet(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	target := Target{OS: "linux", Arch: "amd64"}
	opts := Options{Targets: []Target{target}, Sandbox: Sandbox{Native: target}, Version: "1.0.0", BakedSettings: []byte("name: small\n")}
	output := filepath.Join(t.TempDir(), "pig-release")
	var nested []string
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		inputDir := hostMountSource(args, "/input,")
		if inputDir == "" {
			return nil, fmt.Errorf("missing /input mount: %v", args)
		}
		for i := 0; i+1 < len(args); i++ {
			if args[i] != "--mount" {
				continue
			}
			for field := range strings.SplitSeq(args[i+1], ",") {
				relative, ok := strings.CutPrefix(field, "dst=/input/")
				if !ok {
					continue
				}
				nested = append(nested, relative)
				if _, err := os.Stat(filepath.Join(inputDir, filepath.FromSlash(relative))); err != nil {
					return nil, fmt.Errorf("mountpoint /input/%s is absent when the engine runs: %w", relative, err)
				}
			}
		}
		return nil, writeFakeContainerOutputs(args, output, opts, target)
	}
	builder := testDefaultContainer("v9.8.7", lookPathTable("podman"), run, filepath.Join(t.TempDir(), "container-go"))
	if _, err := builder.Build(context.Background(), BuilderRequest{Piglet: parsed, Options: opts, Output: output, Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if len(nested) == 0 {
		t.Fatal("the local extension produced no nested /input mount")
	}
}
