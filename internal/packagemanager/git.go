package packagemanager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports packages/coding-agent/src/core/package-manager.ts (getLocalGitUpdateTarget, ensureGitRef, cleanAndInstallGitDependencies, repairMissingGitDependencies).
type GitUpdateTarget struct {
	ref       string
	fetchArgs []string
}

func GetLocalGitUpdateTarget(checkout string) (GitUpdateTarget, error) {
	upstream, err := RunPackageCapture(checkout, "git", "rev-parse", "--abbrev-ref", "@{upstream}")
	if branch, ok := strings.CutPrefix(upstream, "origin/"); err == nil && ok && branch != "" {
		if _, err := RunPackageCapture(checkout, "git", "rev-parse", "@{upstream}"); err == nil {
			return GitUpdateTarget{"@{upstream}", []string{"fetch", "--prune", "--no-tags", "origin", "+refs/heads/" + branch + ":refs/remotes/origin/" + branch}}, nil
		}
	}
	// upstream: packages/coding-agent/src/core/package-manager.ts:getLocalGitUpdateTarget
	_ = RunPackageProcess(checkout, "git", "remote", "set-head", "origin", "-a")
	if _, err := RunPackageCapture(checkout, "git", "rev-parse", "origin/HEAD"); err != nil {
		return GitUpdateTarget{}, err
	}
	symbolic, _ := RunPackageCapture(checkout, "git", "symbolic-ref", "refs/remotes/origin/HEAD")
	branch := strings.TrimPrefix(symbolic, "refs/remotes/origin/")
	refspec := "+HEAD:refs/remotes/origin/HEAD"
	if branch != "" {
		refspec = "+refs/heads/" + branch + ":refs/remotes/origin/" + branch
	}
	return GitUpdateTarget{"origin/HEAD", []string{"fetch", "--prune", "--no-tags", "origin", refspec}}, nil
}

func GitUpdateMarkerPath(checkout string) string {
	return filepath.Join(filepath.Dir(checkout), "."+filepath.Base(checkout)+".pi-update-incomplete")
}

func EnsureGitRef(checkout, packageRoot string, sm *codingagent.SettingsManager, target GitUpdateTarget) error {
	if err := RunPackageProcess(checkout, "git", target.fetchArgs...); err != nil {
		return err
	}
	local, err := RunPackageCapture(checkout, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	commitRef := target.ref + "^{commit}"
	remote, err := RunPackageCapture(checkout, "git", "rev-parse", commitRef)
	if err != nil {
		return err
	}
	marker := GitUpdateMarkerPath(checkout)
	if local == remote {
		if _, err := os.Stat(marker); err == nil {
			return CleanAndInstallGitDependencies(checkout, packageRoot, marker, sm)
		}
		return RepairMissingGitDependencies(packageRoot, sm)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		return err
	}
	if err := RunPackageProcess(checkout, "git", "reset", "--hard", commitRef); err != nil {
		return err
	}
	return CleanAndInstallGitDependencies(checkout, packageRoot, marker, sm)
}

func InstallGitDependencies(packageRoot string, sm *codingagent.SettingsManager) error {
	if _, err := os.Stat(filepath.Join(packageRoot, "package.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	command := DefaultNpmCommand(sm)
	args := append([]string{}, command[1:]...)
	args = append(args, GetGitDependencyInstallArgs(sm)...)
	return RunPackageProcess(packageRoot, command[0], args...)
}

func RepairMissingGitDependencies(packageRoot string, sm *codingagent.SettingsManager) error {
	data, err := os.ReadFile(filepath.Join(packageRoot, "package.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Dependencies map[string]json.RawMessage `json:"dependencies"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	root := filepath.Join(packageRoot, "node_modules")
	for name := range manifest.Dependencies {
		path := filepath.Join(root, filepath.FromSlash(name))
		if !strings.HasPrefix(path, root+string(filepath.Separator)) {
			continue
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return InstallGitDependencies(packageRoot, sm)
		}
	}
	return nil
}

func CleanAndInstallGitDependencies(checkout, packageRoot, marker string, sm *codingagent.SettingsManager) error {
	if err := RunPackageProcess(checkout, "git", "clean", "-fdx"); err != nil {
		// upstream: packages/coding-agent/src/core/package-manager.ts:cleanAndInstallGitDependencies
		_ = RepairMissingGitDependencies(packageRoot, sm)
		return err
	}
	if err := RequireGitSubdirectoryWithinCheckout(checkout, packageRoot); err != nil {
		return err
	}
	if err := InstallGitDependencies(packageRoot, sm); err != nil {
		return err
	}
	return RemoveGitUpdateMarker(marker)
}

func RemoveGitUpdateMarker(marker string) error {
	err := os.Remove(marker)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func PruneEmptyGitParents(checkout, root string) error {
	if root == "" {
		return nil
	}
	for current := filepath.Dir(checkout); current != root && strings.HasPrefix(current, root+string(filepath.Separator)); current = filepath.Dir(current) {
		entries, err := os.ReadDir(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			break
		}
		if err := os.Remove(current); err != nil {
			break
		}
	}
	return nil
}
