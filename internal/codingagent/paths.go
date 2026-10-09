package codingagent

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// AppName is the binary/CLI name, independent of the selected configuration directories.
const AppName = "pig"

// PackageName is PiG's npm package: the launcher that a global npm-family
// install owns and that the signed update manifest names. Mirrors upstream
// PACKAGE_NAME export. automation/release/pig_package.py reads this line, so keep
// the declaration on one line.
const PackageName = "@pi-in-go/pig"

// CONFIG_DIR_NAME is the default per-project config directory name.
const CONFIG_DIR_NAME = "." + AppName

// UsePiDirs reports whether the user explicitly selected Pi's configuration directories.
func UsePiDirs() bool {
	// pig divergence (D2): sharing Pi state requires an explicit process-level opt-in.
	return os.Getenv("PIG_USE_PI_DIRS") == "1"
}

// ConfigDirName returns the selected per-project configuration directory name.
func ConfigDirName() string {
	if UsePiDirs() {
		return ".pi"
	}
	return CONFIG_DIR_NAME
}

// ENV_AGENT_DIR overrides the config directory.
// Mirrors upstream ENV_AGENT_DIR export.
const ENV_AGENT_DIR = "PIG_CODING_AGENT_DIR"

// ENV_SESSION_DIR overrides the session storage directory.
// Mirrors upstream ENV_SESSION_DIR export.
const ENV_SESSION_DIR = "PIG_CODING_AGENT_SESSION_DIR"

// pig divergence (D2): title says "PiG", not "pi": separate binary and config root.
const APP_TITLE = "PiG"

// AgentDir returns the writable agent directory. Shared mode uses Pi's environment override; otherwise it uses PiG's. Both expand a leading tilde. It panics
// with a [configroot.UnresolvedError] when the home directory is needed and unavailable; [ResolveAgentDir] reports that as an error.
func AgentDir() string {
	if configured := os.Getenv(agentDirEnvName()); configured != "" {
		return ExpandTildePath(configured)
	}
	return DefaultAgentDir()
}

// ResolveAgentDir is [AgentDir] with an error, never a relative path, when the home directory is needed and unavailable.
func ResolveAgentDir() (string, error) {
	if configured := os.Getenv(agentDirEnvName()); configured != "" {
		return ExpandTildePath(configured), nil
	}
	return ResolveDefaultAgentDir()
}

// AgentDirConfigured reports whether the agent directory comes from its environment variable rather than the default.
func AgentDirConfigured() bool { return os.Getenv(agentDirEnvName()) != "" }

func agentDirEnvName() string {
	if UsePiDirs() {
		return "PI_CODING_AGENT_DIR"
	}
	return ENV_AGENT_DIR
}

// ProjectConfigDir returns the selected workspace-local configuration root.
func ProjectConfigDir(cwd string) string {
	return filepath.Join(cwd, ConfigDirName())
}

// NPMInstallRoot returns the managed npm project for one settings scope.
func NPMInstallRoot(cwd, agentDir string, project bool) string {
	if project {
		return filepath.Join(ProjectConfigDir(cwd), "npm")
	}
	return filepath.Join(agentDir, "npm")
}

// GitInstallRoot returns the managed Git checkout root for one settings scope.
func GitInstallRoot(cwd, agentDir string, project bool) string {
	if project {
		return filepath.Join(ProjectConfigDir(cwd), "git")
	}
	return filepath.Join(agentDir, "git")
}

// CatalogRoot returns the managed catalog materialization root.
func CatalogRoot(agentDir string) string {
	return filepath.Join(agentDir, "catalog")
}

// StateDir returns one additive capability's user-scoped state directory.
// Namespaces are code-owned identifiers, not user-provided paths.
func StateDir(namespace string) string {
	if namespace == "" || namespace == "." || namespace == ".." || filepath.Clean(namespace) != namespace || strings.ContainsAny(namespace, `/\\`) {
		panic("invalid Pig state namespace: " + namespace)
	}
	return filepath.Join(ConfigRoot(), "state", namespace)
}

// ProjectStateDir returns one additive capability's workspace-scoped state directory.
func ProjectStateDir(cwd, namespace string) string {
	if namespace == "" || namespace == "." || namespace == ".." || filepath.Clean(namespace) != namespace || strings.ContainsAny(namespace, `/\\`) {
		panic("invalid Pig state namespace: " + namespace)
	}
	return filepath.Join(ProjectConfigDir(cwd), "state", namespace)
}

// PigletArtifactsDir returns the managed Piglet artifact store.
func PigletArtifactsDir() string {
	return filepath.Join(ConfigRoot(), "artifacts", "piglets")
}

// PigletRecordsDir returns the managed Piglet record store. Records live
// outside Piglet discovery so they never masquerade as editable source.
func PigletRecordsDir() string {
	return filepath.Join(ConfigRoot(), "receipts", "piglets")
}

// PigletsDir returns the user-owned Piglet source directory.
func PigletsDir() string {
	return filepath.Join(ConfigRoot(), "piglets")
}

// SelfUpdateCommand describes the command a proven package-manager installation runs to update itself.
//
// Mirrors upstream SelfUpdateCommand (config.ts). Steps holds an optional
// sequence of sub-commands (e.g. uninstall old name, then install new name).
type SelfUpdateCommand struct {
	Command string
	Args    []string
	Display string
	// Steps holds an optional ordered list of commands to run in sequence.
	// Mirrors upstream SelfUpdateCommand.steps (config.ts v0.73.1).
	Steps []*SelfUpdateCommand
}

func makeSelfUpdateCommandStep(command string, args []string) *SelfUpdateCommand {
	var display strings.Builder
	for i, arg := range append([]string{command}, args...) {
		if i > 0 {
			display.WriteByte(' ')
		}
		if strings.ContainsFunc(arg, isJSWhitespace) {
			display.WriteString(`"` + arg + `"`)
			continue
		}
		display.WriteString(arg)
	}
	return &SelfUpdateCommand{Command: command, Args: args, Display: display.String()}
}

func makeSelfUpdateCommand(installStep, uninstallStep *SelfUpdateCommand) *SelfUpdateCommand {
	if uninstallStep == nil {
		return installStep
	}
	return &SelfUpdateCommand{
		Command: installStep.Command,
		Args:    installStep.Args,
		Display: uninstallStep.Display + " && " + installStep.Display,
		Steps:   []*SelfUpdateCommand{uninstallStep, installStep},
	}
}

// SelfUpdatePackageTarget is the package a self-update installs: its name, and
// the spec the package manager installs, which is the name when empty.
// Mirrors upstream SelfUpdatePackageTarget (config.ts).
type SelfUpdatePackageTarget struct {
	PackageName string
	InstallSpec string
}

func normalizeSelfUpdatePackageTarget(target SelfUpdatePackageTarget) SelfUpdatePackageTarget {
	if target.InstallSpec == "" {
		target.InstallSpec = target.PackageName
	}
	return target
}

// packageManagerSelfUpdateCommand mirrors upstream getSelfUpdateCommandForMethod
// command construction, including --ignore-scripts and package-manager release
// age controls. It uninstalls installedPackageName first only when target
// renames the package.
func packageManagerSelfUpdateCommand(owner PackageManagerOwner, installedPackageName string, npmCommand []string, target SelfUpdatePackageTarget) *SelfUpdateCommand {
	if installedPackageName == "" {
		return nil
	}
	if target.PackageName == "" {
		target.PackageName = installedPackageName
	}
	target = normalizeSelfUpdatePackageTarget(target)

	command := "npm"
	var baseArgs []string
	if len(npmCommand) > 0 {
		command = npmCommand[0]
		baseArgs = append(baseArgs, npmCommand[1:]...)
	}

	switch owner {
	case ownerPNPM:
		return makeSelfUpdateCommand(
			makeSelfUpdateCommandStep("pnpm", []string{"install", "-g", "--ignore-scripts", "--config.minimumReleaseAge=0", target.InstallSpec}),
			selfUpdateUninstallStep("pnpm", []string{"remove", "-g"}, installedPackageName, target.PackageName),
		)
	case ownerYarn:
		return makeSelfUpdateCommand(
			makeSelfUpdateCommandStep("yarn", []string{"global", "add", "--ignore-scripts", target.InstallSpec}),
			selfUpdateUninstallStep("yarn", []string{"global", "remove"}, installedPackageName, target.PackageName),
		)
	case ownerBun:
		return makeSelfUpdateCommand(
			makeSelfUpdateCommandStep("bun", []string{"install", "-g", "--ignore-scripts", "--minimum-release-age=0", target.InstallSpec}),
			selfUpdateUninstallStep("bun", []string{"uninstall", "-g"}, installedPackageName, target.PackageName),
		)
	default:
		installArgs := append(append([]string{}, baseArgs...), "install", "-g", "--ignore-scripts", "--min-release-age=0", target.InstallSpec)
		uninstallPrefix := append([]string{}, baseArgs...)
		return makeSelfUpdateCommand(
			makeSelfUpdateCommandStep(command, installArgs),
			selfUpdateUninstallStep(command, append(uninstallPrefix, "uninstall", "-g"), installedPackageName, target.PackageName),
		)
	}
}

func selfUpdateUninstallStep(command string, argsPrefix []string, installedPackageName, updatePackageName string) *SelfUpdateCommand {
	if updatePackageName == installedPackageName {
		return nil
	}
	args := append(append([]string{}, argsPrefix...), installedPackageName)
	return makeSelfUpdateCommandStep(command, args)
}

// ─── Config Root ──────────────────────────────────────────────────────────────

// ConfigRoot returns the pig configuration root directory (internal/configroot, the one resolver the host and every extension SDK share).
//
// Resolution order:
//  1. $PIG_HOME if set and non-empty
//  2. $XDG_CONFIG_HOME/pig if XDG_CONFIG_HOME is set and non-empty
//  3. ~/.pig (default)
//
// A leading ~ expands to the home directory. PiG-owned state stays here even when the agent and project directories are shared with Pi. It panics with
// a [configroot.UnresolvedError] when the home directory is needed and unavailable.
func ConfigRoot() string { return configroot.Dir() }

// GetDocsPath is the directory of the documentation bundle the system prompt, the login help and the codemode description point at.
// upstream: config.ts:449 getDocsPath (join(getPackageDir(), "docs")); pig additive (D22): PiG materializes its documentation bundle under the configuration root instead of shipping it in the package directory.
func GetDocsPath() string {
	docs := filepath.Join(ConfigRoot(), "docs")
	resolved, err := nodepath.Resolve(docs)
	if err != nil {
		return docs
	}
	return resolved
}

// GetReadmePath is the README of the documentation bundle, the "Main documentation" entry of the system prompt.
// upstream: config.ts:444 getReadmePath (join(getPackageDir(), "README.md")); pig additive (D22): the README is the bundle's own.
func GetReadmePath() string { return filepath.Join(GetDocsPath(), "README.md") }

// GetExamplesPath is where the system prompt says PiG's examples are.
// upstream: config.ts:454 getExamplesPath (join(getPackageDir(), "examples")); pig additive (D22): PiG's examples are published in its repository, so the location is [prompts.PigExamplesLocation], a URL, not a directory.
func GetExamplesPath() string { return prompts.PigExamplesLocation }

// CanonicalizePath resolves a nonempty path to its absolute canonical filesystem form, following symlinks and drive junctions while preserving Windows volume mount points as directories. It preserves the raw input if resolution fails, including an empty or missing path.
// Mirrors upstream canonicalizePath.
func CanonicalizePath(path string) string {
	if path == "" {
		return path
	}
	absolute, err := nodepath.Resolve(path)
	if err != nil {
		return path
	}
	canonical, err := evalCanonicalPath(absolute)
	if err != nil {
		return path
	}
	return canonical
}

// IsLocalPath reports whether value is a local path rather than a package source, a built-in extension or a remote URL. Bare names, relative paths, and file URLs are local.
// Ports .upstream/v0.99.1/packages/coding-agent/src/utils/paths.ts:45-64 (a `builtin:` value names a built-in extension).
func IsLocalPath(value string) bool {
	trimmed := jsTrim(value)
	for _, prefix := range []string{"npm:", "git:", "github:", "http:", "https:", "ssh:", BuiltinPathPrefix} {
		if strings.HasPrefix(trimmed, prefix) {
			return false
		}
	}
	return true
}

// ResolvePath normalizes input and baseDir, then resolves the input to an absolute path. An empty baseDir uses the process working directory. Invalid file URLs return an error.
// Ports packages/coding-agent/src/utils/paths.ts:102-106.
func ResolvePath(input, baseDir string) (string, error) {
	return resolvepath.Resolve(input, baseDir)
}

// ExpandTildePath expands a leading ~ in a filesystem path.
// Mirrors upstream expandTildePath.
func ExpandTildePath(path string) string { return configroot.ExpandTilde(path) }

// resolveAgainstCwd is Pi's resolvePath(filePath, cwd) without input normalization. On Windows Node's isAbsolute accepts a rooted path without a drive (/x or \x), which then resolves on the process's drive, not cwd's.
func resolveAgainstCwd(filePath, cwd string) (string, error) {
	if nodepath.IsAbsolute(filePath) {
		return nodepath.Resolve(filePath)
	}
	return nodepath.Resolve(cwd, filePath)
}

// GetCwdRelativePath returns the path relative to cwd, with the platform
// separator as Pi's path.relative gives it, when filePath resolves inside
// cwd, or "" when it is outside cwd or either path fails to resolve (Pi's
// resolvePath throws for an invalid file URL). Both paths are normalized as
// resolvePath does: a leading ~ expands to the home directory and a file URL
// becomes its path.
func GetCwdRelativePath(filePath, cwd string) string {
	resolvedCwd, err := ResolvePath(cwd, "")
	if err != nil {
		return ""
	}
	resolvedPath, err := ResolvePath(filePath, resolvedCwd)
	if err != nil {
		return ""
	}
	relativePath, err := filepath.Rel(resolvedCwd, resolvedPath)
	if err != nil {
		return ""
	}
	isInsideCwd := relativePath == "." || (relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) && !filepath.IsAbs(relativePath))
	if !isInsideCwd {
		return ""
	}
	return relativePath
}

// FormatPathRelativeToCwdOrAbsolute returns a slash-normalized path relative to
// cwd when possible, otherwise the cleaned absolute path.
func FormatPathRelativeToCwdOrAbsolute(filePath, cwd string) string {
	absolutePath, err := ResolvePath(filePath, cwd)
	if err != nil {
		return filepath.ToSlash(filePath)
	}
	if relativePath := GetCwdRelativePath(absolutePath, cwd); relativePath != "" {
		return filepath.ToSlash(relativePath)
	}
	return filepath.ToSlash(absolutePath)
}

// MarkPathIgnoredByCloudSync best-effort marks a directory as ignored by
// cloud sync providers. Mirrors upstream markPathIgnoredByCloudSync.
func MarkPathIgnoredByCloudSync(path string) {
	var commands [][]string
	switch runtime.GOOS {
	case "darwin":
		commands = [][]string{
			{"xattr", "-w", "com.dropbox.ignored", "1", path},
			{"xattr", "-w", "com.apple.fileprovider.ignore#P", "1", path},
		}
	case "linux":
		commands = [][]string{{"setfattr", "-n", "user.com.dropbox.ignored", "-v", "1", path}}
	default:
		return
	}
	for _, args := range commands {
		cmd := linkerexec.Command(args[0], args[1:]...)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		_ = cmd.Run()
	}
}
