package pigletbuild

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

// fusedModuleGraph is the module graph fused members add to Pig's main module.
type fusedModuleGraph struct {
	// roots are the member and workspace module roots whose go.sum entries the
	// build reads, sorted and unique.
	roots []string
	// raised names each Pig requirement a member raised, for the build log.
	raised []string
	// unvetted names, sorted, each external module a member or workspace
	// module requires as "path@version" at the version the build selects,
	// followed by " => target" when a replacement supplies its source. The
	// fused vet reads only local module source, so it checks none of them.
	unvetted []string
}

// mergeFusedModules adds the fused members' module graph to pig, the build's
// view of Pig's go.mod. A dependency module's requirements and replacements do
// not reach a main module's go.mod, so with GOWORK=off and -mod=readonly a
// member's external dependencies would otherwise need go.mod updates.
//
// The rule is Go's minimum version selection: each module gets the highest
// version Pig or any member requires, so a member never downgrades Pig and may
// raise a Pig requirement, which is reported. Pig itself and its extension SDK
// are host modules: the source being built is the version that is linked, so a
// member requiring a newer SDK than Pig pins cannot be fused into it. Member
// replacements are promoted; two different targets for one module are an error.
func mergeFusedModules(pig *modfile.File, fused []fusedEntry) (fusedModuleGraph, error) {
	if pig.Module == nil {
		return fusedModuleGraph{}, errors.New("pig go.mod has no module directive")
	}
	pigPath := pig.Module.Mod.Path
	sdkPin := ""
	pigVersions := make(map[string]string, len(pig.Require))
	for _, requirement := range pig.Require {
		pigVersions[requirement.Mod.Path] = requirement.Mod.Version
		if requirement.Mod.Path == extsource.GoSDKModulePath {
			sdkPin = requirement.Mod.Version
		}
	}

	// Local modules: each member module and its workspace modules, built from source.
	local := map[string]string{}
	owner := map[string]string{}
	addLocal := func(member, modulePath, root string) error {
		if previous, ok := local[modulePath]; ok && previous != root {
			return fmt.Errorf("fuse %q: module %s is fused from both %s and %s", member, modulePath, previous, root)
		}
		local[modulePath] = root
		if _, ok := owner[root]; !ok {
			owner[root] = member
		}
		return nil
	}
	for _, f := range fused {
		if f.ModulePath == "" || f.Package == "" {
			return fusedModuleGraph{}, fmt.Errorf("fuse %q: module and package paths are required", f.Name)
		}
		if err := addLocal(f.Name, f.ModulePath, f.Root); err != nil {
			return fusedModuleGraph{}, err
		}
		for _, moduleRoot := range f.WorkspaceModules {
			moduleFile, err := readModFile(moduleRoot)
			if err != nil {
				return fusedModuleGraph{}, fmt.Errorf("fuse %q workspace module: %w", f.Name, err)
			}
			if moduleFile.Module == nil {
				return fusedModuleGraph{}, fmt.Errorf("fuse %q workspace module %s has no module path", f.Name, moduleRoot)
			}
			if moduleFile.Module.Mod.Path == f.ModulePath {
				continue
			}
			if err := addLocal(f.Name, moduleFile.Module.Mod.Path, moduleRoot); err != nil {
				return fusedModuleGraph{}, err
			}
		}
	}
	roots := make([]string, 0, len(owner))
	for root := range owner {
		roots = append(roots, root)
	}
	slices.Sort(roots)

	type replacement struct {
		target module.Version
		root   string
	}
	want := map[string]string{}
	wantFrom := map[string]string{}
	replaces := map[module.Version]replacement{}
	for _, root := range roots {
		member := owner[root]
		file, err := readModFile(root)
		if err != nil {
			return fusedModuleGraph{}, fmt.Errorf("fuse %q: %w", member, err)
		}
		for _, requirement := range file.Require {
			path, version := requirement.Mod.Path, requirement.Mod.Version
			switch {
			case path == pigPath:
				// The main module is always the source being built.
				continue
			case path == extsource.GoSDKModulePath:
				if sdkPin != "" && semver.Compare(version, sdkPin) > 0 {
					return fusedModuleGraph{}, fmt.Errorf("fuse %q: %s requires %s %s, newer than the %s this Pig source pins; a fused member links the SDK of the Pig it is fused into, so build with a Pig source at %s or later", member, filepath.Join(root, "go.mod"), path, version, sdkPin, version)
				}
				continue
			case extsource.IsGoSDKModulePath(path):
				continue
			}
			if semver.Compare(version, want[path]) > 0 {
				want[path] = version
				wantFrom[path] = member
			}
		}
		for _, r := range file.Replace {
			if r.Old.Path == pigPath || extsource.IsGoSDKModulePath(r.Old.Path) || extsource.IsGoSDKModulePath(r.New.Path) {
				continue
			}
			if _, ok := local[r.Old.Path]; ok {
				continue
			}
			target := r.New
			if target.Version == "" && !filepath.IsAbs(target.Path) && strings.HasPrefix(target.Path, ".") {
				target.Path = filepath.Clean(filepath.Join(root, target.Path))
			}
			if previous, ok := replaces[r.Old]; ok && previous.target != target {
				return fusedModuleGraph{}, fmt.Errorf("fuse %q: conflicting replacements for %s: %s replaces it with %s, %s with %s", member, formatModule(r.Old), filepath.Join(previous.root, "go.mod"), formatModule(previous.target), filepath.Join(root, "go.mod"), formatModule(target))
			}
			replaces[r.Old] = replacement{target: target, root: root}
		}
	}

	graph := fusedModuleGraph{roots: roots}
	localPaths := make([]string, 0, len(local))
	for path := range local {
		localPaths = append(localPaths, path)
	}
	slices.Sort(localPaths)
	// A local member module keeps the highest version Pig or any member
	// requires, so its source replacement satisfies every minimum.
	for _, path := range localPaths {
		version := "v0.0.0"
		for _, required := range []string{want[path], pigVersions[path]} {
			if semver.Compare(required, version) > 0 {
				version = required
			}
		}
		if err := pig.AddRequire(path, version); err != nil {
			return fusedModuleGraph{}, err
		}
		if err := pig.AddReplace(path, "", local[path], ""); err != nil {
			return fusedModuleGraph{}, err
		}
		delete(want, path)
	}
	paths := make([]string, 0, len(want))
	for path := range want {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		version, current := want[path], pigVersions[path]
		switch {
		case current == "":
			pig.AddNewRequire(path, version, true)
		case semver.Compare(version, current) > 0:
			if err := pig.AddRequire(path, version); err != nil {
				return fusedModuleGraph{}, err
			}
			graph.raised = append(graph.raised, fmt.Sprintf("fused member %q raises Pig's %s %s to %s (minimum version selection)", wantFrom[path], path, current, version))
		}
	}
	olds := make([]module.Version, 0, len(replaces))
	for old := range replaces {
		olds = append(olds, old)
	}
	slices.SortFunc(olds, func(a, b module.Version) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return strings.Compare(a.Version, b.Version)
	})
	for _, old := range olds {
		promoted := replaces[old]
		if existing := pigReplacement(pig, old); existing != nil {
			if existing.New != promoted.target {
				return fusedModuleGraph{}, fmt.Errorf("fuse %q: %s replaces %s with %s, but Pig replaces it with %s", owner[promoted.root], filepath.Join(promoted.root, "go.mod"), formatModule(old), formatModule(promoted.target), formatModule(existing.New))
			}
			continue
		}
		if err := pig.AddReplace(old.Path, old.Version, promoted.target.Path, promoted.target.Version); err != nil {
			return fusedModuleGraph{}, err
		}
	}
	// pig additive (D31): name every external module the fused vet skipped,
	// as the merged go.mod links it.
	for _, path := range paths {
		selected := module.Version{Path: path, Version: want[path]}
		if semver.Compare(pigVersions[path], selected.Version) > 0 {
			selected.Version = pigVersions[path]
		}
		entry := selected.Path + "@" + selected.Version
		replacement := pigReplacement(pig, selected)
		if replacement == nil {
			replacement = pigReplacement(pig, module.Version{Path: path})
		}
		if replacement != nil {
			entry += " => " + replacement.New.Path
			if replacement.New.Version != "" {
				entry += "@" + replacement.New.Version
			}
		}
		graph.unvetted = append(graph.unvetted, entry)
	}
	return graph, nil
}

// pigReplacement returns Pig's replacement of old, if any.
func pigReplacement(pig *modfile.File, old module.Version) *modfile.Replace {
	for _, r := range pig.Replace {
		if r.Old == old {
			return r
		}
	}
	return nil
}

func formatModule(m module.Version) string {
	if m.Version == "" {
		return m.Path
	}
	return m.Path + " " + m.Version
}

func readModFile(root string) (*modfile.File, error) {
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return modfile.Parse(path, data, nil)
}

// mergeGoSum appends the go.sum entries of roots missing from base, so the
// build verifies every fused module without writing a checksum file.
func mergeGoSum(base []byte, roots []string) ([]byte, error) {
	seen := map[string]struct{}{}
	for line := range strings.SplitSeq(string(base), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			seen[line] = struct{}{}
		}
	}
	out := bytes.Clone(base)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	for _, root := range roots {
		data, err := os.ReadFile(filepath.Join(root, "go.sum"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			out = append(append(out, line...), '\n')
		}
	}
	return out, nil
}
