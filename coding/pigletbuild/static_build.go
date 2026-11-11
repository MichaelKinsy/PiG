package pigletbuild

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// cgoPackagesFormat prints the import path of every non-standard-library
// package that has cgo sources. Standard-library packages with cgo sources
// (net, os/user, runtime/cgo) have pure-Go fallbacks and are skipped.
const cgoPackagesFormat = `{{if and (not .Standard) .CgoFiles}}{{.ImportPath}}{{"\n"}}{{end}}`

// withStaticCgoDefault returns env with CGO_ENABLED selected for a Piglet
// Binary build. A CGO_ENABLED the user set, in the environment or with
// `go env -w`, is never replaced (userSet). Otherwise the build is static
// (CGO_ENABLED=0) unless a package of the build has cgo sources (needsCgo),
// in which case the Go default stays. The Piglet Binary then needs no libc
// when nothing in it uses cgo. On Android outside arm64 the linker needs cgo
// even for a cgo-free program, so the choice follows runtimecell.GoBuildCgoEnv.
func withStaticCgoDefault(env []string, target Target, userSet func() (bool, error), needsCgo func() (bool, error)) ([]string, error) {
	if hasEnvKey(env, "CGO_ENABLED") {
		return env, nil
	}
	set, err := userSet()
	if err != nil || set {
		return env, err
	}
	cgo, err := needsCgo()
	if err != nil || cgo {
		return env, err
	}
	return append(slices.Clone(env), runtimecell.GoBuildCgoEnv(target.OS, target.Arch)), nil
}

func hasEnvKey(env []string, key string) bool {
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name == key {
			return true
		}
	}
	return false
}

// goCgoUserSet reports whether the go command's configuration, here the
// `go env -w` file, sets CGO_ENABLED to a value other than the default.
func goCgoUserSet(ctx context.Context, source pigSource, goToolchain toolchain.GoToolchain) (bool, error) {
	out, err := goOutput(source.goCommand(ctx, goToolchain, "env", "-changed", "CGO_ENABLED"))
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// buildNeedsCgo reports whether any non-standard-library package of the Piglet
// Binary build has cgo sources. It lists with cgo on, since a cgo-off listing
// hides the cgo files of exactly the packages the answer depends on. The
// listing uses the build's overlay and tags, so generated fuse imports are
// included and stripped built-ins are not.
func buildNeedsCgo(ctx context.Context, source pigSource, goToolchain toolchain.GoToolchain, overlayPath string, tags []string) (bool, error) {
	args := []string{"list", "-deps", "-f", cgoPackagesFormat}
	if overlayPath != "" {
		args = append(args, "-overlay", overlayPath)
	}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	cmd := source.goCommand(ctx, goToolchain, append(args, "./cmd/pig")...)
	cmd.Env = append(cmd.Env, "CGO_ENABLED=1")
	out, err := goOutput(cmd)
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

func goOutput(cmd *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go %s: %w: %s", cmd.Args[1], err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

// staticPigletBuildEnv returns the environment of the final `go build`: the
// source's build environment with the static CGO default applied. When a probe
// fails (an unresolvable module, a missing package), the environment stays as
// it was: the build that follows hits the same failure and reports it with its
// own diagnostics, and one that does not fail builds as before.
func staticPigletBuildEnv(ctx context.Context, source pigSource, goToolchain toolchain.GoToolchain, build *exec.Cmd, overlayPath string, tags []string, target Target) []string {
	env, err := withStaticCgoDefault(build.Env, target,
		func() (bool, error) { return goCgoUserSet(ctx, source, goToolchain) },
		func() (bool, error) { return buildNeedsCgo(ctx, source, goToolchain, overlayPath, tags) },
	)
	if err != nil {
		return build.Env
	}
	return env
}
