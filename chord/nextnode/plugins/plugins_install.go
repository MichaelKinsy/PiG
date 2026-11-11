package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/src/node/plugins/install.ts. Installing (slice 7c §5, decision "Slice 7c's points" (3); slice 12
// §5, decision "`chord.plugins` is a package's manifest of plugins"): a plugin's npm dependencies as coding-agent installs a
// package's (`npm install` in the plugin's real directory without dev dependencies, host peers or lifecycle scripts; the
// command overridable), and new plugins from git (a shallow clone of a ref) or npm (a packed tarball extracted), each staged
// in a hidden directory of the root, checked as discovery sees it, then renamed into place. Nothing a source supplies reaches
// git or npm as an option: refs, URLs and names starting with `-` are refused, and positional arguments follow `--`.
//
// Every upstream `await` of a child process or file system call is a blocking call here that returns its error; an abort of
// ctx kills the running child (SIGTERM, as Node's `spawn` signal does) and the call returns the abort's reason
// (context.Cause).

// InstallOptions is what installs take: the npm command (`["npm"]` by default, e.g. `["pnpm"]`); git and tar are run as
// `git`, `tar`. A nil NpmCommand is upstream's undefined (npm); an empty, non-nil one is refused.
type InstallOptions struct {
	NpmCommand []string
}

// InstallPluginOptions is what InstallPlugin takes besides the source and the root.
type InstallPluginOptions struct {
	InstallOptions
	// Kinds are the app's facet kinds: a package whose top has a facet file is one plugin (none by default).
	Kinds []string
	// Roots are every root of the app: a new plugin named like one in any of them is refused (`[root]` when nil).
	Roots []model.PluginRoot
}

// PluginSource is where InstallPlugin installs from: a git repository at a ref (a branch, tag or commit), or an npm package.
// Upstream's `{ git, ref } | { npm, version }`: exactly one of Git and Npm is set; a source with no Npm is a git one.
type PluginSource struct {
	Git     string
	Ref     string
	Npm     string
	Version string
}

// installArgs is the install's arguments: production dependencies only, no host peers, no scripts, no audit or funding
// notes.
var installArgs = []string{"install", "--omit=dev", "--legacy-peer-deps", "--ignore-scripts", "--no-audit", "--no-fund"}

// InstallDependencies installs plugin's `dependencies` into its own `node_modules` (`npm install --omit=dev
// --legacy-peer-deps --ignore-scripts --no-audit --no-fund` in its real directory; git dependencies with a ref are npm's).
// It blocks until the command ends and returns an error carrying the command's output when it fails, and the abort's reason
// (context.Cause) when ctx aborts (the command is killed). A single-file plugin has nothing to install.
func InstallDependencies(plugin model.Plugin, ctx context.Context, options InstallOptions) error {
	if plugin.Layout == model.LayoutFile {
		return errors.New(`plugin "` + plugin.Name + `": a single-file plugin has no package.json`)
	}
	command, prefix, err := npmCommandOf(options)
	if err != nil {
		return err
	}
	_, err = run(command, append(append([]string{}, prefix...), installArgs...), plugin.Dir, ctx, `plugin "`+plugin.Name+`": `+command+" install")
	return err
}

// InstallPlugin installs new plugins into root from source, then their dependencies: from git, a shallow clone of the ref
// into `<root>/<repository name>`; from npm, the packed package extracted into `<root>/<name>`. The package's plugins are its
// `chord.plugins` folders, else by convention (discovery's rules). Returns their names. Refused: a source option (`-…`), an
// invalid name, an existing directory (no updating in place: install another ref, or delete and install), a package discovery
// reports a problem in or finds no plugin in, a plugin name another plugin of options.Roots has. Only what this install
// created is removed on failure. It blocks until the install ends.
func InstallPlugin(source PluginSource, root string, ctx context.Context, options InstallPluginOptions) ([]string, error) {
	name, err := targetName(source)
	if err != nil {
		return nil, err
	}
	if err := removeStaleStaging(root); err != nil {
		return nil, err
	}
	made, err := claim(root, name)
	if err != nil {
		return nil, err
	}
	var staging string
	names, err := func() ([]string, error) {
		// Hidden: discovery skips it while it is staged; in the root: the rename stays on one file system.
		var err error
		staging, err = os.MkdirTemp(root, stagingPrefix)
		if err != nil {
			return nil, err
		}
		staged := filepath.Join(staging, "package")
		if source.Npm == "" {
			err = clone(source.Git, source.Ref, staged, ctx)
		} else {
			err = unpack(source.Npm, source.Version, staged, ctx, options.InstallOptions)
		}
		if err != nil {
			return nil, err
		}
		if err := checkPackage(staged, made.target, root, ctx, options); err != nil {
			return nil, err
		}
		// The claimed directory is empty: renaming onto it moves the package into place in one step. Windows renames onto
		// no existing directory: there the claim is released first (a concurrent install of the name may then win).
		if runtime.GOOS == "windows" {
			if err := os.Remove(made.target); err != nil {
				return nil, err
			}
		}
		// `os.Rename` refuses an existing directory target on every platform; Node's `rename` is rename(2), which replaces an
		// empty one.
		if err := syscall.Rename(staged, made.target); err != nil {
			err = &os.LinkError{Op: "rename", Old: staged, New: made.target, Err: err}
			return nil, err
		}
		discovery, err := model.DiscoverPackage(made.target, atRoot(root, options), ctx)
		if err != nil {
			return nil, err
		}
		for _, plugin := range discovery.Plugins {
			if err := InstallDependencies(plugin, ctx, options.InstallOptions); err != nil {
				return nil, err
			}
		}
		names := make([]string, 0, len(discovery.Plugins))
		for _, plugin := range discovery.Plugins {
			names = append(names, plugin.Name)
		}
		return names, nil
	}()
	if err != nil {
		// A removal that fails replaces the install's error, as a throw in upstream's `catch` does.
		if removeErr := os.RemoveAll(made.target); removeErr != nil {
			err = removeErr
		} else if made.scope != "" {
			// Not empty or gone: left, as upstream's `.catch(() => {})` leaves it.
			_ = os.Remove(made.scope)
		}
		names = nil
	}
	// `finally`: a removal that fails replaces the result or the error.
	if staging != "" {
		if removeErr := os.RemoveAll(staging); removeErr != nil {
			return nil, removeErr
		}
	}
	return names, err
}

// stagingPrefix is the prefix of an install's staging directory in a root (hidden: discovery skips it).
const stagingPrefix = ".chord-install-"

// staleAfter is how old a staging directory must be to count as left over by a crashed install.
const staleAfter = 60 * time.Minute

// removeStaleStaging removes staging directories in root an install crashed out of (older than an hour).
func removeStaleStaging(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		// `readdir(root).catch(() => [])`.
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.SortFunc(names, model.CompareUTF16)
	for _, name := range names {
		if !strings.HasPrefix(name, stagingPrefix) {
			continue
		}
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		if err == nil && time.Since(info.ModTime()) > staleAfter {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
	}
	return nil
}

// refuse is upstream's local `option`: a source value that is empty or starts with `-` could be taken for an option.
func refuse(value, what string) error {
	if value == "" || strings.HasPrefix(value, "-") {
		return errors.New("installPlugin: " + what + ` "` + value + `" is refused`)
	}
	return nil
}

// targetName is the directory name a source installs into: the repository's name, or the package's (validated) name.
func targetName(source PluginSource) (string, error) {
	if source.Npm == "" {
		if err := refuse(source.Git, "repository"); err != nil {
			return "", err
		}
		if err := refuse(source.Ref, "ref"); err != nil {
			return "", err
		}
		name := strings.TrimSuffix(nodeBasename(strings.TrimRight(source.Git, "/")), ".git")
		if name == "" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") {
			return "", errors.New(`installPlugin: "` + source.Git + `" names no repository directory`)
		}
		return name, nil
	}
	if err := refuse(source.Npm, "package"); err != nil {
		return "", err
	}
	if err := refuse(source.Version, "version"); err != nil {
		return "", err
	}
	if !host.IsPluginName(source.Npm) {
		return "", errors.New(`installPlugin: "` + source.Npm + `" is not an npm package name`)
	}
	return source.Npm, nil
}

// nodeBasename is Node's `path.basename(path)`: the last element of a path, ignoring trailing separators.
func nodeBasename(path string) string {
	separators := "/"
	if os.PathSeparator == '\\' {
		separators = `/\`
	}
	path = strings.TrimRight(path, separators)
	return path[strings.LastIndexAny(path, separators)+1:]
}

// claimed is what a claim created: the target directory and, when it made one, its scope directory.
type claimed struct {
	target string
	scope  string
}

// claim creates `<root>/<name>` (and a scope directory `<root>/@scope` it needs) without a recursive make, so an existing
// one refuses the install and a concurrent install of the same name loses here; it returns what it created.
func claim(root, name string) (claimed, error) {
	target := filepath.Join(root, name)
	made := claimed{target: target}
	if strings.HasPrefix(name, "@") {
		dir := filepath.Dir(target)
		err := os.Mkdir(dir, 0o777)
		if err == nil {
			made.scope = dir
		} else if !errors.Is(err, fs.ErrExist) {
			return claimed{}, err
		}
	}
	if err := os.Mkdir(target, 0o777); err != nil {
		if made.scope != "" {
			// `rmdir(scope).catch(() => {})`.
			_ = os.Remove(made.scope)
		}
		if errors.Is(err, fs.ErrExist) {
			return claimed{}, errors.New("installPlugin: " + target + " exists already")
		}
		return claimed{}, err
	}
	return made, nil
}

// atRoot is the root a package is discovered as part of.
func atRoot(root string, options InstallPluginOptions) model.RootContext {
	return model.RootContext{Root: root, Required: false, Kinds: options.Kinds}
}

// checkPackage checks the package staged at staged (going to target): discovery reports no problem in it, finds a plugin,
// and none of its plugins' names is taken in the app's roots.
func checkPackage(staged, target, root string, ctx context.Context, options InstallPluginOptions) error {
	discovery, err := model.DiscoverPackage(staged, atRoot(root, options), ctx)
	if err != nil {
		return err
	}
	if len(discovery.Problems) > 0 {
		problem := discovery.Problems[0]
		return errors.New("installPlugin: " + strings.Replace(problem.Path, staged, target, 1) + ": " + problem.Error)
	}
	if len(discovery.Plugins) == 0 {
		return errors.New("installPlugin: " + target + " holds no plugin")
	}
	roots := options.Roots
	if roots == nil {
		roots = []model.PluginRoot{{Path: root}}
	}
	installed, err := model.DiscoverPlugins(roots, model.DiscoveryOptions{Kinds: options.Kinds}, ctx)
	if err != nil {
		return err
	}
	for _, plugin := range discovery.Plugins {
		if i := slices.IndexFunc(installed.Plugins, func(each model.Plugin) bool { return each.Name == plugin.Name }); i >= 0 {
			return errors.New(`installPlugin: plugin "` + plugin.Name + `" is in ` + installed.Plugins[i].Dir + " already")
		}
	}
	return nil
}

// npmCommandOf is the npm command of options and its leading arguments.
func npmCommandOf(options InstallOptions) (string, []string, error) {
	command := options.NpmCommand
	if command == nil {
		command = []string{"npm"}
	}
	if len(command) == 0 {
		return "", nil, errors.New("npmCommand is empty")
	}
	return command[0], command[1:], nil
}

// syncBuffer is a buffer two copying goroutines (the child's stdout and stderr) write to.
type syncBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

// stdoutTee writes to the combined output and to the child's stdout alone.
type stdoutTee struct {
	combined *syncBuffer
	stdout   bytes.Buffer
}

func (t *stdoutTee) Write(p []byte) (int, error) {
	t.stdout.Write(p)
	return t.combined.Write(p)
}

// run runs command in cwd and returns its stdout; the error names what with the command's output when it fails, and is the
// abort's reason (context.Cause) when ctx aborts (the command is sent SIGTERM, as Node's `spawn` signal does).
func run(command string, args []string, cwd string, ctx context.Context, what string) (string, error) {
	if ctx.Err() != nil {
		return "", context.Cause(ctx)
	}
	//nolint:gosec // G204 (class c): the command is the app's npmCommand option (default npm), git or tar, run by Pi's install the same way; every argument after the app's own follows `--` where git or npm take a source's value.
	child := exec.CommandContext(ctx, command, args...)
	child.Dir = cwd
	// Never a prompt for credentials: an install runs unattended.
	child.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	child.Cancel = func() error {
		if err := child.Process.Signal(syscall.SIGTERM); err != nil {
			return child.Process.Kill()
		}
		return nil
	}
	// The output of both streams in the order it was written, and stdout alone.
	output := &syncBuffer{}
	stdout := &stdoutTee{combined: output}
	child.Stdout = stdout
	child.Stderr = output
	// pig divergence (D267): Pi's `spawn` settles on the child's `close`, which waits for the end of its output, so a process the
	// install started that inherited the pipes (a daemon npm left behind) holds the install for as long as it lives. The install
	// ends with the command's exit, and what holds its pipes is waited for only for the stop grace after that.
	child.WaitDelay = defaultStopGrace
	err := child.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		// pig divergence (D267): the command exited with code 0; what it started held its output pipes past the grace.
		err = nil
	}
	if ctx.Err() != nil && err != nil {
		return "", context.Cause(ctx)
	}
	if err == nil {
		return stdout.stdout.String(), nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return "", err
	}
	// Node's `code` is null for a command a signal ended.
	code := "null"
	if exit.ExitCode() >= 0 {
		code = strconv.Itoa(exit.ExitCode())
	}
	return "", fmt.Errorf("%s failed (exit %s): %s", what, code, strings.TrimFunc(output.data.String(), func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }))
}

// clone is a shallow clone of ref (a branch, tag or commit) of url into dir (made), detached at it.
func clone(url, ref, dir string, ctx context.Context) error {
	if err := os.Mkdir(dir, 0o777); err != nil {
		return err
	}
	what := "git clone of " + url + " at " + ref
	if _, err := run("git", []string{"init", "--quiet"}, dir, ctx, what); err != nil {
		return err
	}
	if _, err := run("git", []string{"fetch", "--quiet", "--depth", "1", "--", url, ref}, dir, ctx, what); err != nil {
		return err
	}
	_, err := run("git", []string{"-c", "advice.detachedHead=false", "checkout", "--quiet", "FETCH_HEAD", "--"}, dir, ctx, what)
	return err
}

// unpack is npm package name at version packed (`npm pack`) and extracted into dir (made).
func unpack(name, version, dir string, ctx context.Context, options InstallOptions) error {
	command, prefix, err := npmCommandOf(options)
	if err != nil {
		return err
	}
	packed, err := os.MkdirTemp(filepath.Dir(dir), "pack-")
	if err != nil {
		return err
	}
	what := command + " pack " + name + "@" + version
	args := append(append([]string{}, prefix...), "pack", "--json", "--pack-destination", packed, "--", name+"@"+version)
	stdout, err := run(command, args, packed, ctx, what)
	if err != nil {
		return err
	}
	filename, err := packedTarball(stdout, what)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o777); err != nil {
		return err
	}
	file := filepath.Join(packed, nodeBasename(filename))
	_, err = run("tar", []string{"-xzf", file, "--strip-components=1", "-C", dir}, packed, ctx, "extracting "+name)
	return err
}

// packedTarball is the file name of the first tarball in npm's `pack --json` output. Upstream reads `const [tarball] =
// JSON.parse(output)`: the list of packed tarballs npm 10 and 11 print. Newer npm (12) prints an object keyed by package
// name instead, which that destructuring cannot read; this reads the first member of an object the same way, so one
// install works with either npm.
func packedTarball(output, what string) (string, error) {
	// pig divergence (D501): an object keyed by package name (npm 12's `pack --json`) is read as the list is; Pi's destructuring rejects it.
	value, err := delta.DecodeJson([]byte(output))
	if err != nil {
		return "", err
	}
	var tarball any
	switch typed := value.(type) {
	case []any:
		if len(typed) == 0 {
			return "", errors.New(what + ": packed nothing")
		}
		tarball = typed[0]
	case *delta.JsonObject:
		keys := typed.Keys()
		if len(keys) == 0 {
			return "", errors.New(what + ": packed nothing")
		}
		tarball, _ = typed.Get(keys[0])
	default:
		return "", errors.New("npm's output is not a list of packed tarballs")
	}
	object, ok := tarball.(*delta.JsonObject)
	if !ok {
		return "", errors.New("Cannot read properties of " + map[bool]string{true: "null", false: "a primitive"}[tarball == nil] + " (reading 'filename')")
	}
	filename, _ := object.Get("filename")
	text, ok := filename.(string)
	if !ok {
		return "", errors.New(`The "path" argument must be of type string`)
	}
	return text, nil
}
