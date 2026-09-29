package packagemanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/crossspawn"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

type ConfiguredPackage struct {
	Source        codingagent.PackageSource
	Scope         string
	InstalledPath string
	// ResolvedSource owns the inherited installation when a project delta retains a different metadata source.
	ResolvedSource string
}

func GetGitDependencyInstallArgs(sm *codingagent.SettingsManager) []string {
	configuredCommand := sm.GetNpmCommand()
	if len(configuredCommand) > 0 {
		return []string{"install"}
	}
	return []string{"install", "--omit=dev"}
}

// SettingsBaseDir is the directory a scope's package sources resolve against: the project config directory below cwd, or the agent directory (package-manager.ts getBaseDirForScope).
func SettingsBaseDir(cwd, agentDir string, local bool) string {
	if local {
		return codingagent.ProjectConfigDir(cwd)
	}
	return agentDir
}

func InstallPackageArtifacts(cwd, agentDir string, sm *codingagent.SettingsManager, pkg codingagent.PackageSource, local bool, progress ProgressCallback) error {
	if local && !sm.IsProjectTrusted() {
		return errors.New("Project is not trusted; refusing to access project package storage")
	}
	return WithProgress(progress, "install", pkg.Source, fmt.Sprintf("Installing %s...", pkg.Source), func() error {
		switch DetectSourceKind(pkg.Source) {
		case "local":
			root, err := ResolveInputPackageSourceRoot(cwd, agentDir, sm, pkg.Source)
			if err != nil {
				return err
			}
			if _, err := os.Stat(root); err != nil {
				return fmt.Errorf("Path does not exist: %s", root)
			}
			return nil
		case "npm":
			return InstallManagedNPM(cwd, agentDir, sm, pkg.Source, local)
		case "git":
			return InstallManagedGit(cwd, agentDir, sm, pkg.Source, local)
		default:
			return fmt.Errorf("unsupported package source: %s", pkg.Source)
		}
	})
}

func SourceRootForResources(cwd, agentDir string, sm *codingagent.SettingsManager, source string, local bool) (string, error) {
	switch DetectSourceKind(source) {
	case "npm":
		ref, err := ParseNpmInstallRef(source)
		if err != nil {
			return "", err
		}
		return NpmInstallPath(cwd, agentDir, sm, ref, local), nil
	case "git":
		return GitInstallPath(cwd, agentDir, source, local)
	case "local":
		baseDir := SettingsBaseDir(cwd, agentDir, local)
		return ResolveLocalPackageRoot(baseDir, source)
	default:
		return "", fmt.Errorf("unsupported package source: %s", source)
	}
}

func ResolveInputPackageSourceRoot(cwd, agentDir string, sm *codingagent.SettingsManager, source string) (string, error) {
	if DetectSourceKind(source) != "local" {
		return SourceRootForResources(cwd, agentDir, sm, source, false)
	}
	return ResolveLocalPackageRoot(cwd, source)
}

func ResolveLocalPackageRoot(baseDir, source string) (string, error) {
	return resolvepath.ResolvePackagePath(source, baseDir)
}

func NpmInstallPath(cwd, agentDir string, sm *codingagent.SettingsManager, ref sourceref.Ref, local bool) string {
	installRoot := NpmInstallRoot(cwd, agentDir, ref, local)
	managedPath := filepath.Join(installRoot, "node_modules", filepath.FromSlash(ref.NPMName))
	if local || ref.NPMRegistry != "" {
		return managedPath
	}
	if _, err := os.Stat(managedPath); err == nil {
		return managedPath
	}
	if legacyPath := GetLegacyGlobalNpmInstallPath(sm, ref.NPMName); legacyPath != "" {
		if _, err := os.Stat(legacyPath); err == nil {
			return legacyPath
		}
	}
	return managedPath
}

func NpmInstallRoot(cwd, agentDir string, ref sourceref.Ref, local bool) string {
	root := codingagent.NPMInstallRoot(cwd, agentDir, local)
	if ref.NPMRegistry == "" {
		return root
	}
	digest := sha256.Sum256([]byte(ref.NPMRegistry))
	return filepath.Join(root, "registries", fmt.Sprintf("%x", digest[:8]))
}

// GetLegacyGlobalNpmInstallPath follows Pi's pnpm lookup, then its global-root fallback.
func GetLegacyGlobalNpmInstallPath(sm *codingagent.SettingsManager, packageName string) string {
	if resolved, err := GetPnpmGlobalPackagePath(sm, packageName); err != nil {
		return ""
	} else if resolved != "" {
		return resolved
	}
	root, err := GetGlobalNpmRoot(sm)
	if err != nil || root == "" {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(packageName))
}

// GlobalNpmRoot is Pi's DefaultPackageManager globalNpmRoot and globalNpmRootCommandKey. Pi keeps that cache on each DefaultPackageManager, and creates fresh managers (with empty caches) around the same SettingsManager, for example for every interactive package-update check (interactive-mode.ts:1202-1206). Pig threads the SettingsManager itself through package resolution, so each SettingsManager owns one entry until it is collected; a later lookup with an unchanged command reuses the root where Pi would run it again. The mutex also holds concurrent lookups while the first runs the command, as Pi's synchronous spawn does.
type GlobalNpmRoot struct {
	mu         sync.Mutex
	commandKey string
	root       string
}

var GlobalNpmRoots struct {
	mu      sync.Mutex
	entries map[weak.Pointer[codingagent.SettingsManager]]*GlobalNpmRoot
}

func GlobalNpmRootFor(sm *codingagent.SettingsManager) *GlobalNpmRoot {
	key := weak.Make(sm)
	GlobalNpmRoots.mu.Lock()
	defer GlobalNpmRoots.mu.Unlock()
	if entry := GlobalNpmRoots.entries[key]; entry != nil {
		return entry
	}
	if GlobalNpmRoots.entries == nil {
		GlobalNpmRoots.entries = make(map[weak.Pointer[codingagent.SettingsManager]]*GlobalNpmRoot)
	}
	entry := &GlobalNpmRoot{}
	GlobalNpmRoots.entries[key] = entry
	runtime.AddCleanup(sm, func(key weak.Pointer[codingagent.SettingsManager]) {
		GlobalNpmRoots.mu.Lock()
		delete(GlobalNpmRoots.entries, key)
		GlobalNpmRoots.mu.Unlock()
	}, key)
	return entry
}

// GetGlobalNpmRoot is Pi's getGlobalNpmRoot: the selected command's global root, reused until the npmCommand argv changes. An empty root is not reused, and a failed lookup keeps the previous entry.
func GetGlobalNpmRoot(sm *codingagent.SettingsManager) (string, error) {
	command := DefaultNpmCommand(sm)
	commandKey := strings.Join(command, "\x00")
	entry := GlobalNpmRootFor(sm)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.root != "" && entry.commandKey == commandKey {
		return entry.root, nil
	}
	var root string
	if NpmCommandName(command) == "bun" {
		binDir, err := RunCmd(command[0], append(command[1:], "pm", "bin", "-g")...)
		if err != nil || binDir == "" {
			return "", err
		}
		root = filepath.Join(filepath.Dir(binDir), "install", "global", "node_modules")
	} else {
		var err error
		if root, err = RunCmd(command[0], append(command[1:], "root", "-g")...); err != nil {
			return "", err
		}
	}
	entry.commandKey, entry.root = commandKey, root
	return root, nil
}

func GetPnpmGlobalPackagePath(sm *codingagent.SettingsManager, packageName string) (string, error) {
	npmCommand := DefaultNpmCommand(sm)
	if NpmCommandName(npmCommand) != "pnpm" {
		return "", nil
	}
	output, err := RunCmd(npmCommand[0], append(npmCommand[1:], "list", "-g", "--depth", "0", "--json")...)
	if err != nil {
		return "", err
	}
	var entries []struct {
		Dependencies map[string]struct {
			Path string `json:"path"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(output), &entries); err != nil {
		return "", err
	}
	if entries == nil {
		return "", errors.New("invalid pnpm global package list")
	}
	for _, entry := range entries {
		if dep, ok := entry.Dependencies[packageName]; ok && dep.Path != "" {
			return dep.Path, nil
		}
	}
	return "", nil
}

// DefaultNpmCommand uses the caller's resolved trust and overrides; helper calls must not reload project settings.
func DefaultNpmCommand(sm *codingagent.SettingsManager) []string {
	if cmd := sm.GetNpmCommand(); len(cmd) > 0 {
		return append([]string(nil), cmd...)
	}
	return []string{"npm"}
}

// NpmCommandName uses the executable after the last --, preserves case, and removes only .cmd/.exe suffixes without spawning the command (Pi package-manager.ts:1760-1764).
func NpmCommandName(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}
	idx := -1
	for i, part := range cmd {
		if part == "--" {
			idx = i
		}
	}
	target := cmd[0]
	if idx >= 0 {
		if idx+1 == len(cmd) {
			return ""
		}
		target = cmd[idx+1]
	}
	if target == "" {
		return ""
	}
	base := filepath.Base(target)
	ext := filepath.Ext(base)
	if strings.EqualFold(ext, ".cmd") || strings.EqualFold(ext, ".exe") {
		return strings.TrimSuffix(base, ext)
	}
	return base
}

func InstalledPathForSource(cwd, agentDir string, sm *codingagent.SettingsManager, source string, local bool) string {
	path, err := SourceRootForResources(cwd, agentDir, sm, source, local)
	if err == nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return path
		}
	}
	return ""
}

// PackageSourceIdentity centralizes upstream package identity and Pig contributed scheme identity. Identity matching follows upstream parseSource: an unprefixed value is local.
//
// pig additive (D18): registered contributed source schemes participate in
// the same deterministic identity contract as upstream npm/git/local sources.
func PackageSourceIdentity(baseDir, raw string) string {
	identity, err := PackageSourceIdentityChecked(baseDir, raw)
	if err != nil {
		return "unsupported:" + strings.TrimSpace(raw)
	}
	return identity
}

// PackageSourceIdentityChecked is PackageSourceIdentity, except that a local file: URL fileURLToPath rejects returns that error unwrapped, as Pi's getPackageIdentity and getSourceMatchKey* throw it (package-manager.ts:1373-1393,1690-1705).
func PackageSourceIdentityChecked(baseDir, raw string) (string, error) {
	ref, err := sourceref.Parse(raw, sourceref.Options{
		BaseDir:          baseDir,
		Bare:             sourceref.BareLocal,
		AllowContributed: true,
	})
	if err != nil {
		return "unsupported:" + strings.TrimSpace(raw), nil
	}
	identity, err := ref.Identity(baseDir)
	if err != nil {
		if urlErr, ok := errors.AsType[*nodeurl.Error](err); ok {
			return "", urlErr
		}
		return "unsupported:" + strings.TrimSpace(raw), nil
	}
	return identity, nil
}

// PackageSourcesMatch ports package-manager.ts:1428-1433: the stored settings key, then the input key. Either throws fileURLToPath's error for an invalid file: URL.
func PackageSourcesMatch(cwd, baseDir, stored, input string) (bool, error) {
	left, err := PackageSourceIdentityChecked(baseDir, stored)
	if err != nil {
		return false, err
	}
	right, err := PackageSourceIdentityChecked(cwd, input)
	if err != nil {
		return false, err
	}
	return left == right, nil
}

func ParseNpmInstallRef(source string) (sourceref.Ref, error) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareNPM})
	if err != nil || ref.Kind != sourceref.KindNPM {
		return sourceref.Ref{}, fmt.Errorf("invalid npm package source: %s", source)
	}
	return ref, nil
}

func InstallManagedNPM(cwd, agentDir string, sm *codingagent.SettingsManager, source string, local bool) error {
	ref, err := ParseNpmInstallRef(source)
	if err != nil {
		return err
	}
	installRoot := NpmInstallRoot(cwd, agentDir, ref, local)
	if err := EnsureManagedPackageRoot(installRoot); err != nil {
		return err
	}
	command := DefaultNpmCommand(sm)
	args := append([]string{}, command[1:]...)
	args = append(args, NpmInstallArgs(NpmCommandName(command), ref.Locator, installRoot, ref.NPMRegistry)...)
	return RunPackageProcess("", command[0], args...)
}

func NpmInstallArgs(manager, spec, installRoot, registry string) []string {
	var args []string
	switch manager {
	case "bun":
		args = []string{"install", spec, "--cwd", installRoot, "--omit=peer"}
	case "pnpm":
		args = []string{
			"install", spec, "--prefix", installRoot,
			"--config.auto-install-peers=false",
			"--config.strict-peer-dependencies=false",
			"--config.strict-dep-builds=false",
		}
	default:
		args = []string{"install", spec, "--prefix", installRoot, "--legacy-peer-deps"}
	}
	if registry != "" {
		args = append(args, "--registry", registry)
	}
	return args
}

func EnsureManagedPackageRoot(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	codingagent.MarkPathIgnoredByCloudSync(root)
	ignorePath := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(ignorePath, []byte("*\n!.gitignore\n"), 0o644); err != nil {
			return err
		}
	}
	packageJSON := filepath.Join(root, "package.json")
	if _, err := os.Stat(packageJSON); os.IsNotExist(err) {
		if err := os.WriteFile(packageJSON, []byte("{\n  \"name\": \"pi-extensions\",\n  \"private\": true\n}\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func InstallManagedGit(cwd, agentDir string, sm *codingagent.SettingsManager, source string, local bool) (err error) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return fmt.Errorf("invalid Git package source: %s", source)
	}
	checkout, err := GitCheckoutPath(cwd, agentDir, source, local)
	if err != nil {
		return err
	}
	root := codingagent.GitInstallRoot(cwd, agentDir, local)
	packageRoot, err := GitInstallPath(cwd, agentDir, source, local)
	if err != nil {
		return err
	}
	return InstallGitCheckout(sm, ref, checkout, packageRoot, root)
}

// InstallGitCheckout shares update, dependency repair, and failed-clone cleanup between installed and temporary sources. An empty root leaves temporary-cache parents intact, as Pi does.
func InstallGitCheckout(sm *codingagent.SettingsManager, ref sourceref.Ref, checkout, packageRoot, root string) (err error) {
	if _, statErr := os.Stat(checkout); statErr == nil {
		if err := RequireGitSubdirectoryWithinCheckout(checkout, packageRoot); err != nil {
			return err
		}
		target := GitUpdateTarget{ref: "FETCH_HEAD", fetchArgs: []string{"fetch", "origin", ref.GitRef}}
		if ref.GitRef == "" {
			target, err = GetLocalGitUpdateTarget(checkout)
			if err != nil {
				return err
			}
		}
		return EnsureGitRef(checkout, packageRoot, sm, target)
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if root != "" {
		if err := EnsureManagedCheckoutRoot(root); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(checkout), 0o755); err != nil {
		return err
	}
	if err := RemoveGitUpdateMarker(GitUpdateMarkerPath(checkout)); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(checkout), PruneEmptyGitParents(checkout, root))
		}
	}()
	repo := ref.GitRepo
	if !strings.Contains(repo, "://") && !strings.HasPrefix(repo, "git@") {
		repo = "https://" + repo
	}
	if err := RunPackageProcess("", "git", "clone", GitCloneRepo(runtime.GOOS, repo), checkout); err != nil {
		return err
	}
	if ref.GitRef != "" {
		if err := RunPackageProcess(checkout, "git", "checkout", ref.GitRef); err != nil {
			return err
		}
	}
	info, err := os.Stat(packageRoot)
	if err != nil {
		return fmt.Errorf("Git package subdirectory %q does not exist in %s: %w", ref.GitSubdir, ref.GitRepo, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("Git package subdirectory %q in %s is not a directory", ref.GitSubdir, ref.GitRepo)
	}
	if err := RequireGitSubdirectoryWithinCheckout(checkout, packageRoot); err != nil {
		return err
	}
	return InstallGitDependencies(packageRoot, sm)
}

func GitCheckoutPath(cwd, agentDir, source string, local bool) (string, error) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return "", fmt.Errorf("invalid Git package source: %s", source)
	}
	root := codingagent.GitInstallRoot(cwd, agentDir, local)
	relative, err := GitCheckoutRelative(runtime.GOOS, root, ref)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, relative), nil
}

// GitCheckoutRelative is a Git source's checkout directory below the install
// root: its host, then its repository path. Windows cannot name a directory
// with ':', so there a file:// source's leading drive (C:) becomes the segment
// C, and any other segment containing ':' is refused, since NTFS reads
// name:stream as an alternate data stream.
func GitCheckoutRelative(goos, root string, ref sourceref.Ref) (string, error) {
	segments := append([]string{ref.GitHost}, strings.Split(ref.GitPath, "/")...)
	if goos == "windows" {
		// pig additive (D18): a Windows file URL's drive is a checkout segment.
		if strings.HasPrefix(strings.ToLower(ref.GitRepo), "file://") && IsDriveSegment(segments[1]) {
			segments[1] = segments[1][:1]
		}
		for _, segment := range segments {
			if strings.Contains(segment, ":") {
				return "", fmt.Errorf("Refusing to use path outside package install root: %s", filepath.Join(append([]string{root}, segments...)...))
			}
		}
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := ResolveManagedPackagePath(absoluteRoot, segments...)
	if err != nil {
		return "", err
	}
	return filepath.Rel(absoluteRoot, resolved)
}

// GitCloneRepo is the URL git clones for repo. Git for Windows reads
// file://localhost/C:/... as the UNC path //localhost/C:/..., so there a
// localhost file URL with a drive becomes the equivalent file:///C:/....
func GitCloneRepo(goos, repo string) string {
	const localhost = "file://localhost/"
	if goos != "windows" || len(repo) < len(localhost) || !strings.EqualFold(repo[:len(localhost)], localhost) {
		return repo
	}
	rest := repo[len(localhost):]
	drive, _, _ := strings.Cut(rest, "/")
	if !IsDriveSegment(drive) {
		return repo
	}
	return "file:///" + rest
}

// IsDriveSegment reports whether segment is a Windows drive such as C:.
func IsDriveSegment(segment string) bool {
	return len(segment) == 2 && segment[1] == ':' && ('A' <= segment[0] && segment[0] <= 'Z' || 'a' <= segment[0] && segment[0] <= 'z')
}

func GitInstallPath(cwd, agentDir, source string, local bool) (string, error) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return "", fmt.Errorf("invalid Git package source: %s", source)
	}
	checkout, err := GitCheckoutPath(cwd, agentDir, source, local)
	if err != nil {
		return "", err
	}
	if ref.GitSubdir == "" {
		return checkout, nil
	}
	return filepath.Join(checkout, filepath.FromSlash(ref.GitSubdir)), nil
}

func RequireGitSubdirectoryWithinCheckout(checkout, packageRoot string) error {
	resolvedCheckout, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		return fmt.Errorf("resolve Git checkout %s: %w", checkout, err)
	}
	resolvedPackage, err := filepath.EvalSymlinks(packageRoot)
	if err != nil {
		return fmt.Errorf("resolve Git package root %s: %w", packageRoot, err)
	}
	relative, err := filepath.Rel(resolvedCheckout, resolvedPackage)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("Git package subdirectory %s resolves outside checkout %s", packageRoot, checkout)
	}
	return nil
}

func EnsureManagedCheckoutRoot(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	codingagent.MarkPathIgnoredByCloudSync(root)
	ignorePath := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		return os.WriteFile(ignorePath, []byte("*\n!.gitignore\n"), 0o644)
	}
	return nil
}

func DetectSourceKind(source string) string {
	// Pi package-manager.ts:1446-1470 treats unprefixed sources as local, whether or not the path exists.
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareLocal, AllowContributed: true})
	if err != nil {
		return "unsupported"
	}
	switch ref.Kind {
	case sourceref.KindNPM:
		return "npm"
	case sourceref.KindGit:
		return "git"
	case sourceref.KindLocal:
		return "local"
	case sourceref.KindContributed:
		if installresolver.SupportsSourceScheme(ref.Scheme) {
			return ref.Scheme
		}
	}
	return "unsupported"
}

func RunCmd(name string, args ...string) (string, error) {
	return RunCmdInDir("", name, args...)
}

// RunCmdInDir is Pi's runCommandSync. It starts the command as upstream's spawnProcess does, so a Windows .cmd shim such as npm.cmd receives its arguments exactly, and returns the trimmed stdout, or stderr when stdout is empty, so a warning on stderr cannot corrupt a reported path.
func RunCmdInDir(dir, name string, args ...string) (string, error) {
	cmd := crossspawn.Command(context.Background(), dir, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := stderr.String()
		if detail == "" {
			detail = stdout.String()
		}
		return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(detail))
	}
	output := stdout.String()
	if output == "" {
		output = stderr.String()
	}
	return strings.TrimSpace(output), nil
}
