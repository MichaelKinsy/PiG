//go:build parity

package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// prepareChangelogFixture changes data only: Go embeds the pinned document through a build overlay,
// while Pi reads a private package view through its own PI_PACKAGE_DIR override (config.ts:389-394,455-456).
// Builds finish before terminal timing starts. Neither checkout nor installed Pi files are modified.
func prepareChangelogFixture(ctx context.Context, t *testing.T, sc *Scenario, pig, pi BinaryRef) (BinaryRef, BinaryRef, error) {
	t.Helper()
	if sc.ChangelogFixture == "" {
		return pig, pi, nil
	}
	root, err := findRepoRoot()
	if err != nil {
		return pig, pi, err
	}
	data, err := os.ReadFile(resolveScenarioCWD(sc.SourcePath, sc.ChangelogFixture))
	if err != nil {
		return pig, pi, err
	}
	tmp := t.TempDir()
	fixture := filepath.Join(tmp, "CHANGELOG.md")
	if err := os.WriteFile(fixture, data, 0o600); err != nil {
		return pig, pi, err
	}
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(root, "CHANGELOG.md"): fixture}})
	if err != nil {
		return pig, pi, err
	}
	overlayPath := filepath.Join(tmp, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		return pig, pi, err
	}
	pig.Path = filepath.Join(tmp, "pig")
	cmd := exec.CommandContext(ctx, "go", "build", "-overlay", overlayPath, "-o", pig.Path, "./cmd/pig")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return pig, pi, fmt.Errorf("build changelog fixture: %w\n%s", err, out)
	}
	packageRoot, err := piPackageRoot(pi.Path)
	if err != nil {
		return pig, pi, err
	}
	packageDir := filepath.Join(tmp, "pi-package")
	if err := os.Mkdir(packageDir, 0o700); err != nil {
		return pig, pi, err
	}
	entries, err := os.ReadDir(packageRoot)
	if err != nil {
		return pig, pi, err
	}
	for _, entry := range entries {
		if entry.Name() == "CHANGELOG.md" {
			continue
		}
		if err := os.Symlink(filepath.Join(packageRoot, entry.Name()), filepath.Join(packageDir, entry.Name())); err != nil {
			return pig, pi, err
		}
	}
	if err := os.WriteFile(filepath.Join(packageDir, "CHANGELOG.md"), data, 0o600); err != nil {
		return pig, pi, err
	}
	pi.Env = slices.DeleteFunc(slices.Clone(pi.Env), func(value string) bool { return strings.HasPrefix(value, "PI_PACKAGE_DIR=") })
	pi.Env = append(pi.Env, "PI_PACKAGE_DIR="+packageDir)
	return pig, pi, nil
}
