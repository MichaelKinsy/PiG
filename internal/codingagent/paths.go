package codingagent

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// AppName is the binary/CLI name. Distinct from upstream pi to avoid
// PATH collisions and shared-config corruption.
const AppName = "pig"

// PackageName identifies the installed package/binary for self-update messaging.
// Mirrors upstream PACKAGE_NAME export.
const PackageName = "pig"

// CONFIG_DIR_NAME is the per-project config directory name.
// Mirrors upstream's CONFIG_DIR_NAME export.
const CONFIG_DIR_NAME = "." + AppName

// ENV_AGENT_DIR overrides the config directory.
// Mirrors upstream ENV_AGENT_DIR export.
const ENV_AGENT_DIR = "PIG_CODING_AGENT_DIR"

// ENV_SESSION_DIR overrides the session storage directory.
// Mirrors upstream ENV_SESSION_DIR export.
const ENV_SESSION_DIR = "PIG_CODING_AGENT_SESSION_DIR"

// pig divergence (D2): title says "PiG", not "pi": separate binary and config root.
const APP_TITLE = "PiG"

// AgentDir returns the configured writable agent directory. The environment
// override matches the directory selected by the main CLI and RPC mode.
func AgentDir() string {
	if configured := os.Getenv(ENV_AGENT_DIR); configured != "" {
		return configured
	}
	return DefaultAgentDir()
}

// ProjectConfigDir returns the workspace-local Pig configuration root.
func ProjectConfigDir(cwd string) string {
	return filepath.Join(cwd, CONFIG_DIR_NAME)
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

// SelfUpdateCommand describes the command a managed install would run to
// update itself. pig returns nil because it ships as a standalone binary, not
// a global npm/pnpm/yarn package.
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
	display.WriteString(command)
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\n\r") {
			display.WriteString(` "` + arg + `"`)
			continue
		}
		display.WriteString(" " + arg)
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
func packageManagerSelfUpdateCommand(installedPackageName string, npmCommand []string, target SelfUpdatePackageTarget) *SelfUpdateCommand {
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

	switch command {
	case "pnpm":
		return makeSelfUpdateCommand(
			makeSelfUpdateCommandStep("pnpm", []string{"install", "-g", "--ignore-scripts", "--config.minimumReleaseAge=0", target.InstallSpec}),
			selfUpdateUninstallStep("pnpm", []string{"remove", "-g"}, installedPackageName, target.PackageName),
		)
	case "yarn":
		return makeSelfUpdateCommand(
			makeSelfUpdateCommandStep("yarn", []string{"global", "add", "--ignore-scripts", target.InstallSpec}),
			selfUpdateUninstallStep("yarn", []string{"global", "remove"}, installedPackageName, target.PackageName),
		)
	case "bun":
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

// ConfigRoot returns the pig configuration root directory.
//
// Resolution order:
//  1. $PIG_HOME if set and non-empty
//  2. $XDG_CONFIG_HOME/pig if XDG_CONFIG_HOME is set
//  3. ~/.pig (default)
//
// All pig state: agent dir, auth.json, sessions, agents, subagent output -
// lives under this root. Never touches ~/.pi/.
func ConfigRoot() string {
	if v := os.Getenv("PIG_HOME"); v != "" {
		return ExpandTildePath(v)
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(ExpandTildePath(v), "pig")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pig")
}

// CanonicalizePath resolves a path to its canonical filesystem form,
// following symlinks. If resolution fails (for example because the path
// does not exist yet), it falls back to the raw path.
// Mirrors upstream canonicalizePath.
func CanonicalizePath(path string) string {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return canonical
}

// ExpandTildePath expands a leading ~ in a filesystem path.
// Mirrors upstream expandTildePath.
func ExpandTildePath(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return path
}

func resolveAgainstCwd(filePath, cwd string) string {
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}
	return filepath.Clean(filepath.Join(cwd, filePath))
}

// GetCwdRelativePath returns the slash-normalized path relative to cwd when
// filePath resolves inside cwd, or "" when it is outside cwd.
func GetCwdRelativePath(filePath, cwd string) string {
	resolvedCwd := filepath.Clean(cwd)
	resolvedPath := resolveAgainstCwd(filePath, resolvedCwd)
	relativePath, err := filepath.Rel(resolvedCwd, resolvedPath)
	if err != nil {
		return ""
	}
	isInsideCwd := relativePath == "." || (relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) && !filepath.IsAbs(relativePath))
	if !isInsideCwd {
		return ""
	}
	if relativePath == "." {
		return "."
	}
	return filepath.ToSlash(relativePath)
}

// FormatPathRelativeToCwdOrAbsolute returns a slash-normalized path relative to
// cwd when possible, otherwise the cleaned absolute path.
func FormatPathRelativeToCwdOrAbsolute(filePath, cwd string) string {
	absolutePath := resolveAgainstCwd(filePath, cwd)
	if relativePath := GetCwdRelativePath(absolutePath, cwd); relativePath != "" {
		return relativePath
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
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		_ = cmd.Run()
	}
}

// GetSelfUpdateCommand returns nil without a resolved provenance and exact
// release. The self-update tier resolver supplies those facts before invoking
// PackageManagerUpdateCommand; this generic path must not invent an unpinned
// package-manager mutation.
//
// updatePackageName is the new package name when renaming a package during
// update (mirrors upstream getSelfUpdateCommand updatePackageName param, v0.73.1).
func GetSelfUpdateCommand(_ string, _ []string, _ ...string) *SelfUpdateCommand {
	return nil
}

// GetSelfUpdateUnavailableInstruction returns the fallback self-update hint.
// pig divergence (D39): pig is a standalone binary, so the instruction points at
// `pig update self` (configured update source) or the download/container ladder,
// not an npm package-manager command.
func GetSelfUpdateUnavailableInstruction(_ string, _ []string, _ ...string) string {
	return SelfUpdateFallback()
}

// GetUpdateInstruction returns the user-facing self-update instruction.
func GetUpdateInstruction(packageName string, npmCommand []string) string {
	if cmd := GetSelfUpdateCommand(packageName, npmCommand); cmd != nil {
		return "Run: " + cmd.Display
	}
	return GetSelfUpdateUnavailableInstruction(packageName, npmCommand)
}
