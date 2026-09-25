package pigletbuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/internal/buildprogress"
)

func writeBuilderConfig(t *testing.T, home, body string) {
	t.Helper()
	path := filepath.Join(home, "state", "pigletbuild", "builders.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadContainerBuilderConfigsRequiresDigestPinnedImage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	writeBuilderConfig(t, home, `{"builders":[{"name":"docker","engine":"docker","image":"example.com/pig-builder:latest"}]}`)
	if _, err := loadContainerBuilderConfigs(); err == nil || !strings.Contains(err.Error(), "digest-pinned") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadContainerBuilderConfigsRejectsRemovedVersionField(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	writeBuilderConfig(t, home, `{"version":1,"builders":[]}`)
	if _, err := loadContainerBuilderConfigs(); err == nil || !strings.Contains(err.Error(), "unknown field \"version\"") {
		t.Fatalf("error = %v, want removed version field rejection", err)
	}
}

func TestLoadContainerBuilderConfigsRejectsSecretsAndDuplicateNames(t *testing.T) {
	for _, body := range []string{
		`{"builders":[{"name":"docker","engine":"docker","image":"user:token@example.com/pig@sha256:` + strings.Repeat("a", 64) + `"}]}`,
		`{"builders":[{"name":"docker","engine":"docker","image":"example.com/pig@sha256:` + strings.Repeat("a", 64) + `"},{"name":"docker","engine":"podman","image":"example.com/pig@sha256:` + strings.Repeat("b", 64) + `"}]}`,
	} {
		home := t.TempDir()
		t.Setenv("PIG_HOME", home)
		writeBuilderConfig(t, home, body)
		if _, err := loadContainerBuilderConfigs(); err == nil {
			t.Fatalf("config accepted: %s", body)
		}
	}
}

func TestConfiguredBuildersPreservesConfiguredOrderBeforeNative(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	body := `{"builders":[` +
		`{"name":"docker","engine":"docker","image":"example.com/docker@sha256:` + strings.Repeat("a", 64) + `"},` +
		`{"name":"podman","engine":"podman","image":"example.com/podman@sha256:` + strings.Repeat("b", 64) + `"}` +
		`]}`
	writeBuilderConfig(t, home, body)
	builders, err := configuredBuilders()
	if err != nil {
		t.Fatal(err)
	}
	if got := builderNames(builders); got != "docker, podman, native" {
		t.Fatalf("builder order = %q", got)
	}
}

func TestConfiguredBuildersLeavesRawPigNativeOnlyWithoutConfig(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	builders, err := configuredBuilders()
	if err != nil {
		t.Fatal(err)
	}
	if len(builders) != 1 || builders[0].Name() != "native" {
		t.Fatalf("builders = %v", builderNames(builders))
	}
}

func TestLocalizeContainerPigletUsesOnlyResolvedReadOnlyInputs(t *testing.T) {
	root := t.TempDir()
	extension := filepath.Join(root, "extension")
	sdkReplacement := filepath.Join(root, "sdk")
	skill := filepath.Join(root, "skill")
	prompt := filepath.Join(root, "prompt.md")
	if err := os.MkdirAll(extension, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sdkReplacement, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extension, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extension, "go.mod"), []byte("module example.com/trace\n\ngo 1.26\n\nreplace example.com/sdk => ../sdk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkReplacement, "sdk.go"), []byte("package sdk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: review\n---\nReview."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prompt, []byte("Prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(root, "piglet.yaml")
	pigletData := "name: release\nextensions:\n  - name: trace\n    origins: [local:./extension]\nskills:\n  - name: review\n    origins: [local:./skill]\nsystemPrompt:\n  file: ./prompt.md\n"
	if err := os.WriteFile(pigletPath, []byte(pigletData), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	localized, mounts, err := localizeContainerPiglet(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := piglet.ParseBytes(localized); err != nil {
		t.Fatalf("localized Piglet violates source schema: %v\n%s", err, localized)
	}
	wantTargets := map[string]string{
		extension:      "/input/sources/extensions/trace/extension",
		skill:          "/input/sources/skills/review",
		prompt:         "/input/sources/prompts/prompt.md",
		sdkReplacement: "/input/sources/extensions/trace/sdk",
	}
	for source, target := range wantTargets {
		resolved, _ := filepath.EvalSymlinks(source)
		if !slices.ContainsFunc(mounts, func(mount containerPigletMount) bool {
			return mount.Source == resolved && mount.Target == target
		}) {
			t.Fatalf("localized mounts missing %s -> %s: %v", resolved, target, mounts)
		}
	}
	for _, want := range []string{
		"local:./sources/extensions/trace/extension",
		"local:./sources/skills/review",
		"file: ./sources/prompts/prompt.md",
	} {
		if !strings.Contains(string(localized), want) {
			t.Fatalf("localized Piglet missing %q:\n%s", want, localized)
		}
	}
	if parsed.SystemPrompt == nil || parsed.SystemPrompt.File != "./prompt.md" {
		t.Fatalf("localization mutated source Piglet prompt: %#v", parsed.SystemPrompt)
	}
	if strings.Contains(string(localized), "./extension") || strings.Contains(string(localized), "./skill") || strings.Contains(string(localized), "./prompt.md") {
		t.Fatalf("localized Piglet retained relative host paths:\n%s", localized)
	}
}

func TestContainerBuilderBuildProducesHostArtifactAndContainerIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	pigletPath := filepath.Join(t.TempDir(), "release.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{OS: "linux", Arch: "amd64"}
	opts := Options{Targets: []Target{target}, Sandbox: Sandbox{Native: target}, Version: "1.0.0", BakedSettings: []byte("name: release\n")}
	output := filepath.Join(t.TempDir(), "pig-release")
	var runArgs []string
	builder := containerBuilder{
		config:   ContainerBuilderConfig{Name: "docker", Engine: "docker", Image: "example.com/pig@sha256:" + strings.Repeat("a", 64)},
		lookPath: func(string) (string, error) { return "/usr/bin/docker", nil },
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			runArgs = append([]string(nil), args...)
			outputDir := hostMountSource(args, "/out")
			if outputDir == "" {
				return nil, fmt.Errorf("missing output mount")
			}
			artifact := filepath.Join(outputDir, filepath.Base(output))
			artifactData := []byte("container-artifact")
			if err := os.WriteFile(artifact, artifactData, 0o755); err != nil {
				return nil, err
			}
			componentPlan, err := pigletartifact.BuildPlan(nil)
			if err != nil {
				return nil, err
			}
			resolution, err := pigletartifact.NewResolutionRecord("release", opts.Version, time.Unix(1, 0), pigletartifact.ResolutionInput{
				SourceDigest: "sha256:" + strings.Repeat("b", 64), EffectiveDigest: digestBytes(opts.BakedSettings), ComponentPlan: componentPlan,
			})
			if err != nil {
				return nil, err
			}
			binary, err := pigletartifact.NewBinaryRecord("release", opts.Version, time.Unix(2, 0), resolution, pigletartifact.BinaryInput{
				Target: target.String(), PigVersion: "test", PigSourceRevision: "revision", PigSourceDigest: "sha256:" + strings.Repeat("c", 64),
				Builder: "native", BuilderIdentity: "native:revision", Toolchains: map[string]string{"go": "go version test"},
				Artifact:     pigletartifact.Artifact{Digest: digestBytes(artifactData), Size: int64(len(artifactData)), FileName: "pig-release"},
				Verification: pigletartifact.Verification{Policy: "basic", Passed: true, Checks: []string{"artifact-sha256", "artifact-version-smoke"}},
			})
			if err != nil {
				return nil, err
			}
			recordHome := hostMountSource(args, "/records-home")
			for name, record := range map[string]pigletartifact.Record{"resolution.json": resolution, "binary.json": binary} {
				data, err := json.Marshal(record)
				if err != nil {
					return nil, err
				}
				path := filepath.Join(recordHome, "receipts", "piglets", name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return nil, err
				}
				if err := os.WriteFile(path, data, 0o644); err != nil {
					return nil, err
				}
			}
			return []byte("container build\n"), nil
		},
	}
	var phases []string
	ctx := buildprogress.Observe(context.Background(), func(event buildprogress.Event) { phases = append(phases, event.Phase) }, true)
	result, err := builder.Build(ctx, BuilderRequest{Piglet: parsed, Options: opts, Output: output, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Preparing container build", "Building in container", "Verifying container artifact", "Writing binary and records"}; !slices.Equal(phases, want) {
		t.Fatalf("phases=%v want=%v", phases, want)
	}
	if !slices.Contains(runArgs, "--verbose") {
		t.Fatalf("inner builder lost verbosity: %v", runArgs)
	}
	if result.Builder != "docker" || result.Artifact != output {
		t.Fatalf("result = %#v", result)
	}
	recordData, err := os.ReadFile(result.Record)
	if err != nil {
		t.Fatal(err)
	}
	record, err := pigletartifact.ParseRecord(recordData)
	if err != nil {
		t.Fatal(err)
	}
	if record.Binary.Builder != "docker" || record.Binary.BuilderIdentity != "container:docker:"+builder.config.Image {
		t.Fatalf("record builder = %q identity=%q", record.Binary.Builder, record.Binary.BuilderIdentity)
	}
	joined := strings.Join(runArgs, " ")
	for _, required := range []string{"run --rm --pull=never", "--platform linux/amd64", "dst=/out", "dst=/input,readonly", "dst=/records-home", "--entrypoint pig " + builder.config.Image + " piglet build"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("run args missing %q: %s", required, joined)
		}
	}
	// A Windows host maps no uid:gid; the container runs as the image's user.
	if hasUser := slices.Contains(runArgs, "--user"); hasUser != (runtime.GOOS != "windows") {
		t.Fatalf("run args --user present = %t on %s: %s", hasUser, runtime.GOOS, joined)
	}
	if strings.Contains(joined, "docker.sock") {
		t.Fatalf("run args mount the host Docker socket: %s", joined)
	}
}

func hostMountSource(args []string, target string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--mount" || !strings.Contains(args[i+1], "dst="+target) {
			continue
		}
		for field := range strings.SplitSeq(args[i+1], ",") {
			if source, ok := strings.CutPrefix(field, "src="); ok {
				return source
			}
		}
	}
	return ""
}

func TestContainerBuilderProbeSupportsDockerAndPodman(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			var executable string
			builder := containerBuilder{
				config:   ContainerBuilderConfig{Name: engine, Engine: engine, Image: "example.com/pig@sha256:" + strings.Repeat("a", 64)},
				lookPath: func(name string) (string, error) { executable = name; return "/usr/bin/" + name, nil },
				run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
					if len(args) > 1 && args[0] == "image" && args[1] == "inspect" {
						return []byte("linux/amd64 1"), nil
					}
					return []byte("ok"), nil
				},
			}
			readiness := builder.Probe(context.Background(), BuilderRequest{Options: Options{Targets: []Target{{OS: "linux", Arch: "amd64"}}}})
			if !readiness.Ready || executable != engine {
				t.Fatalf("readiness=%+v executable=%q", readiness, executable)
			}
		})
	}
}

func TestContainerBuilderProbeDoesNotPullOrAuthenticate(t *testing.T) {
	var calls [][]string
	builder := containerBuilder{
		config:   ContainerBuilderConfig{Name: "docker", Engine: "docker", Image: "example.com/pig@sha256:" + strings.Repeat("a", 64)},
		lookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string(nil), args...))
			if len(args) > 1 && args[0] == "image" && args[1] == "inspect" {
				return []byte("linux/amd64 1"), nil
			}
			return []byte("ok"), nil
		},
	}
	readiness := builder.Probe(context.Background(), BuilderRequest{Options: Options{Targets: []Target{{OS: "linux", Arch: "amd64"}}}})
	if !readiness.Ready {
		t.Fatalf("readiness = %+v", readiness)
	}
	if len(calls) != 2 || calls[0][0] != "info" || calls[1][0] != "image" || calls[1][1] != "inspect" {
		t.Fatalf("probe calls = %v", calls)
	}
	for _, call := range calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "pull") || strings.Contains(joined, "login") || strings.Contains(joined, "run") {
			t.Fatalf("probe performed a mutating/auth action: %v", calls)
		}
	}
}

func TestContainerBuilderProbeAcceptsDigestPinnedImageWithoutProtocolVersion(t *testing.T) {
	builder := containerBuilder{
		config:   ContainerBuilderConfig{Name: "docker", Engine: "docker", Image: "example.com/pig@sha256:" + strings.Repeat("a", 64)},
		lookPath: func(string) (string, error) { return "/usr/bin/docker", nil },
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) > 1 && args[0] == "image" && args[1] == "inspect" {
				return []byte("linux/amd64"), nil
			}
			return []byte("ok"), nil
		},
	}
	readiness := builder.Probe(context.Background(), BuilderRequest{Options: Options{Targets: []Target{{OS: "linux", Arch: "amd64"}}}})
	if !readiness.Ready {
		t.Fatalf("readiness = %+v", readiness)
	}
}

func TestContainerBuilderProbeRejectsWrongLocalImagePlatform(t *testing.T) {
	builder := containerBuilder{
		config:   ContainerBuilderConfig{Name: "docker", Engine: "docker", Image: "example.com/pig@sha256:" + strings.Repeat("a", 64)},
		lookPath: func(string) (string, error) { return "/usr/bin/docker", nil },
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) > 1 && args[0] == "image" && args[1] == "inspect" {
				return []byte("linux/arm64 1"), nil
			}
			return []byte("ok"), nil
		},
	}
	readiness := builder.Probe(context.Background(), BuilderRequest{Options: Options{Targets: []Target{{OS: "linux", Arch: "amd64"}}}})
	if readiness.Ready || readiness.Code != "image-platform-unavailable" || !strings.Contains(readiness.Remedy, "pull --platform linux/amd64") {
		t.Fatalf("readiness = %+v", readiness)
	}
}

func TestContainerBuilderProbeReportsMissingLocalImage(t *testing.T) {
	builder := containerBuilder{
		config:   ContainerBuilderConfig{Name: "docker", Engine: "docker", Image: "example.com/pig@sha256:" + strings.Repeat("a", 64)},
		lookPath: func(string) (string, error) { return "/usr/bin/docker", nil },
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) > 0 && args[0] == "info" {
				return []byte("ok"), nil
			}
			return nil, os.ErrNotExist
		},
	}
	readiness := builder.Probe(context.Background(), BuilderRequest{Options: Options{Targets: []Target{{OS: "linux", Arch: "amd64"}}}})
	if readiness.Ready || readiness.Code != "image-unavailable" || !strings.Contains(readiness.Remedy, "pull") {
		t.Fatalf("readiness = %+v", readiness)
	}
}

// A container running as an arbitrary numeric uid has no passwd entry, so
// user.Current fails there. That is the ordinary case for CI and for Kubernetes
// pods, which is where a container build actually runs, so the identity must
// still resolve from the kernel's own view of the ids.
func TestContainerUserIdentityResolvesWithoutAPasswdEntry(t *testing.T) {
	noPasswdEntry := func() (*user.User, error) {
		return nil, errors.New("user: unknown userid 1000")
	}
	identity, err := containerIdentityFrom(noPasswdEntry, func() int { return 1000 }, func() int { return 1000 })
	if err != nil {
		t.Fatalf("containerIdentityFrom: %v", err)
	}
	if identity != "1000:1000" {
		t.Fatalf("identity = %q, want \"1000:1000\"", identity)
	}
}

// A passwd entry, when there is one, still wins.
func TestContainerUserIdentityPrefersThePasswdEntry(t *testing.T) {
	identity, err := containerIdentityFrom(
		func() (*user.User, error) { return &user.User{Uid: "501", Gid: "20"}, nil },
		func() int { return 1000 }, func() int { return 1000 })
	if err != nil {
		t.Fatalf("containerIdentityFrom: %v", err)
	}
	if identity != "501:20" {
		t.Fatalf("identity = %q, want \"501:20\"", identity)
	}
}

// On Windows the build container runs as the image's default user: no --user
// argument, and neither the account nor the numeric ids are looked up.
func TestContainerUserArgsOnWindowsLookUpNoIdentity(t *testing.T) {
	args, err := containerUserArgsFor("windows",
		func() (*user.User, error) { t.Fatal("looked up the account on Windows"); return nil, nil },
		func() int { t.Fatal("read the uid on Windows"); return -1 },
		func() int { t.Fatal("read the gid on Windows"); return -1 })
	if err != nil || args != nil {
		t.Fatalf("containerUserArgsFor(windows) = %q, %v; want no --user and no error", args, err)
	}
}

// Elsewhere the container runs as the invoking user's uid:gid.
func TestContainerUserArgsPassTheInvokingUser(t *testing.T) {
	args, err := containerUserArgsFor("linux",
		func() (*user.User, error) { return &user.User{Uid: "501", Gid: "20"}, nil },
		func() int { return 1000 }, func() int { return 1000 })
	if err != nil || !slices.Equal(args, []string{"--user", "501:20"}) {
		t.Fatalf("containerUserArgsFor(linux) = %q, %v; want --user 501:20", args, err)
	}
}

// With neither a passwd entry nor numeric ids, it must fail loudly rather
// than emit a nonsense --user value.
func TestContainerUserIdentityFailsWithNoIdentityAtAll(t *testing.T) {
	_, err := containerIdentityFrom(
		func() (*user.User, error) { return nil, errors.New("no entry") },
		func() int { return -1 }, func() int { return -1 })
	if err == nil {
		t.Fatal("expected an error when no identity can be resolved")
	}
}
