package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/buildprogress"
)

const pigModulePath = "github.com/MichaelKinsy/PiG"

func runBuildCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "build" {
		return -1
	}
	if len(args) > 1 && (args[1] == "-h" || args[1] == "--help") {
		_, _ = fmt.Fprint(stdout, `Usage: pig build [--verbose]

Build the pig binary from the current source checkout and install it to
$PIG_BIN, or ~/.local/bin/pig when PIG_BIN is unset.

Run this from inside the github.com/MichaelKinsy/PiG source tree, or from a
checkout root containing ./pig.
Use --verbose to stream toolchain output. Progress is written to stderr.
`)
		return 0
	}
	verbose := false
	for _, arg := range args[1:] {
		if arg != "--verbose" {
			_, _ = fmt.Fprintf(stderr, "unknown pig build argument %q\n", arg)
			return 2
		}
		verbose = true
	}
	// pig additive (D18): explicit builds report real work without changing compiler inputs.
	progress := buildprogress.New(stderr, verbose)
	defer progress.Close()
	ctx := buildprogress.Observe(context.Background(), progress.Handle, verbose)
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", progress.Failure(err))
		return 1
	}
	buildprogress.Phase(ctx, "Resolving source", "Locating the PiG checkout and output path")

	root, err := findPigSourceRoot()
	if err != nil {
		return fail(fmt.Errorf("%w; run from the pig checkout or set PIG_SOURCE_ROOT=/path/to/github.com/MichaelKinsy/PiG", err))
	}
	target, err := pigBuildTarget()
	if err != nil {
		return fail(err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fail(err)
	}

	build := gitShortCommit(root)
	ldflags := fmt.Sprintf("-s -w -X main.Build=%s", build)
	buildprogress.Phase(ctx, "Compiling and linking Go binary", "PiG → "+target+" (includes module resolution)")
	buildArgs := buildprogress.ToolArgs(ctx, "go", []string{"build", "-buildvcs=false", "-trimpath", "-ldflags", ldflags, "-o", target, "./cmd/pig"})
	cmd := exec.CommandContext(ctx, "go", buildArgs...)
	cmd.Dir = root
	if err := buildprogress.Run(buildprogress.Member(ctx, "pig"), cmd); err != nil {
		return fail(fmt.Errorf("go build: %w", err))
	}
	if runtime.GOOS == "darwin" {
		signPigBuild(ctx, progress, target, stderr)
	}
	info, err := os.Stat(target)
	if err != nil {
		return fail(err)
	}
	progress.Close()
	_, _ = fmt.Fprintf(stdout, "installed: %s → %s\n", builtPigVersion(target), target)
	reportActivePigBinary(target, stdout)
	progress.Success(target, info.Size())
	return 0
}

func signPigBuild(ctx context.Context, progress *buildprogress.Reporter, target string, stderr io.Writer) {
	codesign, err := exec.LookPath("codesign")
	if err != nil {
		return
	}
	buildprogress.Phase(ctx, "Signing binary", "Ad-hoc codesign: "+target)
	cmd := exec.CommandContext(ctx, codesign, "--force", "--sign", "-", target)
	if err := buildprogress.Run(buildprogress.Member(ctx, "pig"), cmd); err != nil {
		// pig additive (D18): ad-hoc signing remains best-effort, but its failure is visible.
		_, _ = fmt.Fprintf(stderr, "warning: %v\n", progress.Failure(fmt.Errorf("ad-hoc codesign: %w", err)))
	}
}

func reportActivePigBinary(target string, stdout io.Writer) {
	active, err := exec.LookPath("pig")
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "warning: no pig found on PATH; add %s to PATH or set PIG_BIN to your active binary\n", filepath.Dir(target))
		return
	}
	targetReal := resolvedPath(target)
	activeReal := resolvedPath(active)
	if targetReal == activeReal {
		_, _ = fmt.Fprintf(stdout, "active: %s\n", active)
		return
	}
	_, _ = fmt.Fprintf(stdout, "warning: your shell resolves pig to %s, not %s\n", active, target)
	_, _ = fmt.Fprintf(stdout, "         run PIG_BIN=%q pig build, update PATH, or run hash -r after changing shells\n", active)
}

func resolvedPath(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		return real
	}
	return filepath.Clean(path)
}

func pigBuildTarget() (string, error) {
	if target := os.Getenv("PIG_BIN"); target != "" {
		return filepath.Clean(target), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("cannot resolve home directory; set PIG_BIN")
	}
	// Windows starts a program by name only when it has an executable extension.
	name := "pig"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, ".local", "bin", name), nil
}

func findPigSourceRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root, ok := findPigSourceRootFrom(cwd); ok {
		return root, nil
	}
	if root, ok := findPigSourceRootFrom(filepath.Join(cwd, "pig")); ok {
		return root, nil
	}
	for _, candidate := range pigSourceRootCandidates() {
		if root, ok := findPigSourceRootFrom(candidate); ok {
			return root, nil
		}
	}
	return "", errors.New("pig build must be run from inside the github.com/MichaelKinsy/PiG source checkout")
}

func findPigSourceRootFrom(start string) (string, bool) {
	if start == "" {
		return "", false
	}
	cwd, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	for {
		modPath := filepath.Join(cwd, "go.mod")
		data, err := os.ReadFile(modPath)
		if err == nil && strings.Contains(string(data), "module "+pigModulePath) {
			return cwd, true
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			return "", false
		}
		cwd = parent
	}
}

func pigSourceRootCandidates() []string {
	var candidates []string
	if root := os.Getenv("PIG_SOURCE_ROOT"); root != "" {
		candidates = append(candidates, root)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".pig", "source"))
	}
	return candidates
}

func gitShortCommit(root string) string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "dev"
	}
	build := strings.TrimSpace(string(out))
	if build == "" {
		return "dev"
	}
	return build
}

// builtPigVersion names the installed binary from the fields of `pig version`,
// which every PiG build prints.
func builtPigVersion(path string) string {
	if out, err := exec.Command(path, "version").Output(); err == nil {
		if identity, ok := pigVersionIdentity(string(out)); ok {
			return identity
		}
	}
	return "pig"
}

// pigVersionIdentity reads detailedVersionString output into "pig X+Y", the
// composite version (coding.Version).
func pigVersionIdentity(out string) (string, bool) {
	var pig, pi string
	for line := range strings.Lines(out) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), ": ")
		switch key {
		case "pig":
			pig = value
		case "upstream pi":
			pi = value
		}
	}
	switch {
	case pig == "":
		return "", false
	case pi == "":
		return "pig " + pig, true
	default:
		return fmt.Sprintf("pig %s+%s", pig, pi), true
	}
}
