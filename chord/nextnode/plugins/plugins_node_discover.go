package plugins

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"

	"golang.org/x/mod/semver"
)

// nodeEnvVar names the environment variable that selects the Node executable explicitly.
const nodeEnvVar = "PIG_NODE"

// versionsPerSource bounds how many installed versions of one version manager's directory are started to read their
// version: the newest ones, because a directory of versions is searched newest first and an older one cannot satisfy the
// minimum when the newest does not.
const versionsPerSource = 3

// systemNodeDirs are the directories of a unix whose `node` is a package manager's or an installer's, after PATH. A test
// replaces them; a background start may be reading them then, so they are read atomically.
var systemNodeDirs = func() *atomic.Pointer[[]string] {
	var dirs atomic.Pointer[[]string]
	dirs.Store(&[]string{"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin"})
	return &dirs
}()

// discoverNode finds the Node subprocesses run on, at least minimumNode, by this order of precedence:
//
//  1. PIG_NODE, an executable (a path, or a name found on PATH). When it is set it is the only candidate: a value that is
//     not an executable, does not run or is older than minimumNode is the error, and no other Node is tried.
//  2. `node` on PATH.
//  3. The installs of version managers and installers that PATH may not reach (a GUI or a service starts without the
//     shell's PATH): nvm ($NVM_BIN, then the newest of $NVM_DIR/versions/node or ~/.nvm/versions/node), fnm (the newest of
//     its node-versions directory), Volta (~/.volta/bin), asdf (~/.asdf/shims), mise (~/.local/share/mise/shims), then
//     /usr/local/bin, /opt/homebrew/bin and /usr/bin; on Windows %NVM_SYMLINK%, the versions of %NVM_HOME% or
//     %APPDATA%\nvm, fnm's, Volta's (%LOCALAPPDATA%\Volta\bin), %ProgramFiles%\nodejs and %LOCALAPPDATA%\Programs\nodejs.
//
// Each candidate runs `--version` through command; the first that prints minimumNode or newer wins. A candidate that is too
// old does not end the search; when none satisfies, the error names the newest version found.
//
// Stock PiG selects no Node runtime for plugins other than these: the TypeScript extension runtime slot holds Bun and
// executable runtimes that host extensions, not Node, so it is no source here.
func discoverNode(ctx context.Context, command func(context.Context, string, ...string) *exec.Cmd) (string, error) {
	// pig additive (D221): Node discovery beyond PATH (PIG_NODE and the install locations of version managers); Stock PiG
	// finds an existing Node and selects, ships and installs none.
	if command == nil {
		command = exec.CommandContext
	}
	requirement := fmt.Sprintf("evaluating plugins needs Node.js %s or newer", strings.TrimPrefix(minimumNode, "v"))
	if explicit := os.Getenv(nodeEnvVar); explicit != "" {
		path, err := exec.LookPath(explicit)
		if err != nil {
			return "", fmt.Errorf("%s; %s=%q is not an executable: %w", requirement, nodeEnvVar, explicit, err)
		}
		_, problem, err := checkNodeVersion(ctx, command, path)
		if err != nil {
			return "", err
		}
		if problem != "" {
			return "", fmt.Errorf("%s; %s=%s: %s", requirement, nodeEnvVar, path, problem)
		}
		return path, nil
	}

	var newest, firstProblem string
	seen := map[string]bool{}
	try := func(path string) (bool, error) {
		path = filepath.Clean(path)
		if seen[path] {
			return false, nil
		}
		seen[path] = true
		version, problem, err := checkNodeVersion(ctx, command, path)
		if err != nil {
			return false, err
		}
		switch {
		case problem == "":
			return true, nil
		case version != "":
			if newest == "" || semver.Compare(version, newest) > 0 {
				newest = version
			}
		case firstProblem == "":
			firstProblem = problem
		}
		return false, nil
	}
	if path, err := exec.LookPath("node"); err == nil {
		if found, err := try(path); err != nil || found {
			return path, err
		}
	}
	sources := nodeSources()
	var places []string
	for _, source := range sources {
		if !slices.Contains(places, source.dir) {
			places = append(places, source.dir)
		}
		for _, path := range source.paths {
			if found, err := try(path); err != nil || found {
				return path, err
			}
		}
	}
	switch {
	case newest != "":
		return "", fmt.Errorf("%s; found %s", requirement, newest)
	case firstProblem != "":
		return "", fmt.Errorf("%s; %s", requirement, firstProblem)
	}
	return "", fmt.Errorf("%s; node was not found on PATH or in %s; set %s to the path of a Node executable to use another", requirement, strings.Join(places, ", "), nodeEnvVar)
}

// checkNodeVersion runs `path --version`. problem is empty when the version satisfies minimumNode; otherwise it says why not, and
// version is the parsed version of a Node that is too old. err is the context's cause when it ended the probe.
func checkNodeVersion(ctx context.Context, command func(context.Context, string, ...string) *exec.Cmd, path string) (version, problem string, err error) {
	output, runErr := command(ctx, path, "--version").CombinedOutput()
	if runErr != nil {
		if ctx.Err() != nil {
			return "", "", context.Cause(ctx)
		}
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = runErr.Error()
		}
		return "", "node --version failed: " + detail, nil
	}
	found := strings.TrimSpace(string(output))
	if !semver.IsValid(found) {
		return "", fmt.Sprintf("node --version returned %q", found), nil
	}
	if semver.Compare(found, minimumNode) < 0 {
		return found, "found " + found, nil
	}
	return found, "", nil
}

// nodeSource is a place Node may be installed: dir is what an error names, paths are the executables in it that exist, in
// the order they are tried.
type nodeSource struct {
	dir   string
	paths []string
}

// nodeSources lists the install locations of version managers and installers (discoverNode's third source), in order.
func nodeSources() []nodeSource {
	home, _ := os.UserHomeDir()
	under := func(elem ...string) string {
		if home == "" {
			return ""
		}
		return filepath.Join(append([]string{home}, elem...)...)
	}
	// first is the first non-empty of the values: a variable's value, else the default place.
	first := func(values ...string) string {
		for _, value := range values {
			if value != "" {
				return value
			}
		}
		return ""
	}
	var sources []nodeSource
	one := func(dir string) {
		if dir == "" {
			return
		}
		if path, err := exec.LookPath(filepath.Join(dir, "node")); err == nil {
			sources = append(sources, nodeSource{dir: dir, paths: []string{path}})
		} else {
			sources = append(sources, nodeSource{dir: dir})
		}
	}
	// versions lists the newest installs below root: each name of root that is a version, as a directory with the
	// executable at <name>/<rest>.
	versions := func(root string, rest ...string) {
		if root == "" {
			return
		}
		sources = append(sources, nodeSource{dir: root, paths: newestVersions(root, rest)})
	}

	if runtime.GOOS == "windows" {
		if dir := os.Getenv("NVM_SYMLINK"); dir != "" {
			one(dir)
		}
		versions(first(os.Getenv("NVM_HOME"), joinIf(os.Getenv("APPDATA"), "nvm")))
		versions(first(joinIf(os.Getenv("FNM_DIR"), "node-versions"), joinIf(os.Getenv("LOCALAPPDATA"), "fnm", "node-versions")), "installation")
		one(first(joinIf(os.Getenv("VOLTA_HOME"), "bin"), joinIf(os.Getenv("LOCALAPPDATA"), "Volta", "bin")))
		one(joinIf(os.Getenv("ProgramFiles"), "nodejs"))
		one(joinIf(os.Getenv("LOCALAPPDATA"), "Programs", "nodejs"))
		return sources
	}

	one(os.Getenv("NVM_BIN"))
	versions(first(joinIf(os.Getenv("NVM_DIR"), "versions", "node"), under(".nvm", "versions", "node")), "bin")
	fnmDefault := under(".local", "share", "fnm", "node-versions")
	if runtime.GOOS == "darwin" {
		fnmDefault = under("Library", "Application Support", "fnm", "node-versions")
	}
	versions(first(joinIf(os.Getenv("FNM_DIR"), "node-versions"), joinIf(os.Getenv("XDG_DATA_HOME"), "fnm", "node-versions"), fnmDefault), "installation", "bin")
	one(first(joinIf(os.Getenv("VOLTA_HOME"), "bin"), under(".volta", "bin")))
	one(first(joinIf(os.Getenv("ASDF_DATA_DIR"), "shims"), under(".asdf", "shims")))
	one(first(joinIf(os.Getenv("MISE_DATA_DIR"), "shims"), joinIf(os.Getenv("XDG_DATA_HOME"), "mise", "shims"), under(".local", "share", "mise", "shims")))
	for _, dir := range *systemNodeDirs.Load() {
		one(dir)
	}
	return sources
}

// joinIf joins the elements below base, or gives "" when base is empty (a variable that is not set).
func joinIf(base string, elem ...string) string {
	if base == "" {
		return ""
	}
	return filepath.Join(append([]string{base}, elem...)...)
}

// newestVersions returns the executables <root>/<name>/<rest...>/node of the versionsPerSource newest names of root that
// are versions (`v22.19.0`) and have one.
func newestVersions(root string, rest []string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	type install struct{ version, name string }
	var installs []install
	for _, entry := range entries {
		version := entry.Name()
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if entry.IsDir() && semver.IsValid(version) {
			installs = append(installs, install{version, entry.Name()})
		}
	}
	slices.SortFunc(installs, func(a, b install) int { return semver.Compare(b.version, a.version) })
	var paths []string
	for _, found := range installs {
		path, err := exec.LookPath(filepath.Join(append([]string{root, found.name}, append(rest, "node")...)...))
		if err != nil {
			continue
		}
		paths = append(paths, path)
		if len(paths) == versionsPerSource {
			break
		}
	}
	return paths
}
