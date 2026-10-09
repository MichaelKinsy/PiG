package pigletbuild

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// defaultContainerBuilderName is the built-in container builder. Auto selection tries it after
// the native builder, so it builds when the host lacks Go, a Pig source, or the requested target.
const defaultContainerBuilderName = "container"

// defaultContainerImage is the digest-pinned Go image of the ci-go CI image
// (automation/images/ci-go/Dockerfile GO_IMAGE). It is public, so the builder works without a
// registry login.
const defaultContainerImage = "golang:1.27.2-alpine3.24@sha256:85dc1069ac644ea3c527b177303a406eb3358192816cd7f9e5848eb658851673"

// containerEngineEnv overrides the built-in container builder's engine choice.
const containerEngineEnv = "PIG_CONTAINER_ENGINE"

// containerBootstrap makes a container builder install the running PiG release inside a Go
// image before the inner native build, so the image needs no Pig of its own.
type containerBootstrap struct {
	// runningRelease reports the fetchable release version; nil reads the binary's build identity.
	runningRelease func() (string, bool)
	// cacheDir is the host directory mounted as the container's Go module and build cache.
	cacheDir string
}

func (b containerBootstrap) release() (string, bool) {
	if b.runningRelease != nil {
		return b.runningRelease()
	}
	return runningPigRelease()
}

// defaultContainerBuilder is the built-in `container` builder.
// pig additive (D18): a built-in container builder runs the native build in Docker or Podman.
func defaultContainerBuilder() containerBuilder {
	return containerBuilder{
		config:    ContainerBuilderConfig{Name: defaultContainerBuilderName, Engine: "auto", Image: defaultContainerImage},
		bootstrap: &containerBootstrap{cacheDir: filepath.Join(codingagent.ConfigRoot(), "cache", "container-go")},
	}
}

// resolveDefaultEngine selects PIG_CONTAINER_ENGINE when set, else Podman, else Docker.
func resolveDefaultEngine(lookPath executableLookup) (string, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if engine := strings.TrimSpace(os.Getenv(containerEngineEnv)); engine != "" {
		if engine != "docker" && engine != "podman" {
			return "", fmt.Errorf("%s must be docker or podman, not %q", containerEngineEnv, engine)
		}
		if _, err := lookPath(engine); err != nil {
			return "", fmt.Errorf("%s=%s: %w", containerEngineEnv, engine, err)
		}
		return engine, nil
	}
	for _, name := range []string{"podman", "docker"} {
		if _, err := lookPath(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("neither podman nor docker is on PATH")
}

// bootstrapRunArgs returns the engine run arguments that install the release and start its
// inner build with buildArgs. Images are pulled when missing because the default image is public.
func (b containerBootstrap) bootstrapRunArgs(version string, buildArgs []string) (runOptions, command []string) {
	runOptions = []string{
		"--mount", containerMount(b.cacheDir, "/pig-go-cache", false),
		"--env", "GOPATH=/pig-go-cache/path", "--env", "GOMODCACHE=/pig-go-cache/mod", "--env", "GOCACHE=/pig-go-cache/build",
		"--env", "GOFLAGS=-modcacherw", "--env", "GOTOOLCHAIN=local", "--env", "CGO_ENABLED=0",
		"--entrypoint", "sh",
	}
	script := `set -e; GOBIN=/tmp/pig-bin go install ` + pigModulePath + `/cmd/pig@"$0"; exec /tmp/pig-bin/pig "$@"`
	command = append([]string{"-c", script, version}, buildArgs...)
	return runOptions, command
}
