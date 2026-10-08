package pigletbuild

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

// promoteFusedReplacements copies each fused module's local replace directives into the build's view of Pig's
// go.mod: Go ignores a dependency's replace directives, and go mod tidy cannot infer them. Requirements need no
// copying; the tidy step before the build resolves them, including the version bumps a fused module's own
// dependencies force on Pig's pins. Replacements of the SDK, of Pig itself and of fused modules are left to the
// pinned local roots; a module root without a go.mod contributes nothing.
func promoteFusedReplacements(parsed *modfile.File, fused []fusedEntry) error {
	roots := fusedModuleRoots(fused)
	files := make(map[string]*modfile.File, len(roots))
	local := map[string]bool{}
	if parsed.Module != nil {
		local[parsed.Module.Mod.Path] = true
	}
	for _, root := range roots {
		file, err := readModFile(root)
		if err != nil {
			return err
		}
		if file == nil {
			continue
		}
		files[root] = file
		if file.Module != nil {
			local[file.Module.Mod.Path] = true
		}
	}
	for _, root := range roots {
		file := files[root]
		if file == nil {
			continue
		}
		for _, replacement := range file.Replace {
			oldPath, newPath := replacement.Old.Path, replacement.New.Path
			if local[oldPath] || extsource.IsGoSDKModulePath(oldPath) || extsource.IsGoSDKModulePath(newPath) {
				continue
			}
			// A relative replacement is relative to the module that declares it.
			if replacement.New.Version == "" && !filepath.IsAbs(newPath) && strings.HasPrefix(newPath, ".") {
				newPath = filepath.ToSlash(filepath.Clean(filepath.Join(root, newPath)))
			}
			if err := parsed.AddReplace(oldPath, replacement.Old.Version, newPath, replacement.New.Version); err != nil {
				return fmt.Errorf("fuse module %s replace %s: %w", root, oldPath, err)
			}
		}
	}
	return nil
}

// goWorkspaceEnv reports whether env selects a Go workspace (GOWORK naming a file), where -modfile and
// go mod tidy are unavailable. The last GOWORK entry wins, as it does for the command.
func goWorkspaceEnv(env []string) bool {
	workspace := false
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, "GOWORK="); ok {
			workspace = value != "" && value != "off"
		}
	}
	return workspace
}

func fusedModuleRoots(fused []fusedEntry) []string {
	var roots []string
	for _, f := range fused {
		roots = append(roots, f.Root)
		roots = append(roots, f.WorkspaceModules...)
	}
	return slices.Compact(slices.Sorted(slices.Values(roots)))
}

// readModFile parses root/go.mod; a missing file is nil.
func readModFile(root string) (*modfile.File, error) {
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fuse module %s: %w", root, err)
	}
	file, err := modfile.Parse(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("fuse module %s: %w", root, err)
	}
	return file, nil
}

// fusedGoSum returns the union of Pig's go.sum and every fused module's, sorted, so the tidy step before the
// build verifies the modules those files already vouch for without consulting the checksum database.
func fusedGoSum(sourceRoot string, fused []fusedEntry) ([]byte, error) {
	seen := map[string]bool{}
	var lines []string
	for _, root := range append([]string{sourceRoot}, fusedModuleRoots(fused)...) {
		data, err := os.ReadFile(filepath.Join(root, "go.sum"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s go.sum: %w", root, err)
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !seen[line] {
				seen[line] = true
				lines = append(lines, line)
			}
		}
	}
	if len(lines) == 0 {
		return nil, nil
	}
	slices.Sort(lines)
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}
