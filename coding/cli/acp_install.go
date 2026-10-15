package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

const acpPackageSource = "npm:@pi-in-go/pigpen-acp"

// acpSourceRel is where the Package ships the pig-acp source, relative to its root.
var acpSourceRel = filepath.Join("extensions", "acp", "cmd", "pig-acp")

// runACPCommand handles `pig acp install`. pig additive (D18): the Pigpen release flow publishes no prebuilt
// pig-acp, so this builds the source the installed Package ships with the local Go toolchain; it needs no network.
func runACPCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "acp" {
		return -1
	}
	if len(args) != 2 || args[1] != "install" {
		_, _ = fmt.Fprintln(stderr, "Usage: pig acp install\n\nBuild pig-acp from the installed "+acpPackageSource+" Package and put it in the PiG bin directory ($PIG_BIN's directory, else ~/.local/bin).")
		if len(args) == 2 && (args[1] == "-h" || args[1] == "--help") {
			return 0
		}
		return 2
	}
	cwd, agentDir, sm, err := packageContext()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig acp install: %v\n", err)
		return 1
	}
	src := ""
	for _, local := range []bool{false, true} {
		if root := packagemanager.InstalledPathForSource(cwd, agentDir, sm, acpPackageSource, local); root != "" {
			if _, err := os.Stat(filepath.Join(root, acpSourceRel, "go.mod")); err == nil { //perf:ok cold: one stat per `pig acp install`
				src = filepath.Join(root, acpSourceRel)
				break
			}
		}
	}
	if src == "" {
		_, _ = fmt.Fprintf(stderr, "pig acp install: the %s Package is not installed; run `pig install %s` first\n", acpPackageSource, acpPackageSource)
		return 1
	}
	target, err := acpBuildTarget()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig acp install: %v\n", err)
		return 1
	}
	if err := buildACP(context.Background(), src, target, stderr); err != nil { //portlint:allow ctxbackground CLI entry point owns the build
		_, _ = fmt.Fprintf(stderr, "pig acp install: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "installed pig-acp: %s\n", target)
	if _, err := exec.LookPath("pig-acp"); err != nil {
		_, _ = fmt.Fprintf(stdout, "%s is not on your PATH; add it, or use the full path as the editor command.\n", filepath.Dir(target))
	}
	return 0
}

func acpBuildTarget() (string, error) {
	pig, err := pigBuildTarget()
	if err != nil {
		return "", err
	}
	name := "pig-acp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(pig), name), nil
}

func buildACP(ctx context.Context, src, target string, stderr io.Writer) error {
	goTool, err := toolchain.ResolveGo()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("building pig-acp needs Go %s or newer, and none was found; run `pig setup go` or install it from https://go.dev/dl/: %w", toolchain.MinGoVersion, err)
		}
		return err
	}
	if goTool.BelowFloor() {
		return errors.New(goTool.FloorRemedy())
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	cmd := linkerexec.CommandContext(ctx, goTool.Command, "build", "-buildvcs=false", "-trimpath", "-o", target, ".")
	cmd.Dir = src
	cmd.Env = toolchain.WorkDirEnv(src, goTool.Environ(os.Environ()))
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	return nil
}
