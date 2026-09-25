package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/crossspawn"
)

type packageCommand string

const (
	packageInstall packageCommand = "install"
	packageRemove  packageCommand = "remove"
	packageUpdate  packageCommand = "update"
	packageList    packageCommand = "list"
)

type packageCLIOptions struct {
	command         packageCommand
	source          string
	sources         []string
	local           bool
	allPackages     bool // pig update --all: self-update plus every configured package
	extensionsOnly  bool // pig update --extensions: update every package, not pig itself
	selfOnly        bool
	extensionSource string
	force           bool
	validateOnly    bool
	jsonOutput      bool
	help            bool
	invalidOption   string
	missingValue    string
	conflict        string
}

type configuredPackage struct {
	Source        codingagent.PackageSource
	ProjectDelta  *codingagent.PackageSource
	Scope         string
	InstalledPath string
}

type ProgressCallback func(step string)

func packageScopeName(local bool) string {
	if local {
		return "project"
	}
	return "global"
}

// isSelfUpdateTarget reports whether an update target names pig itself.
// Mirrors upstream `source === "self" || source === "<app-name>"`
// (package-manager-cli.ts): pig accepts "self" and the app name "pig". These
// are synonyms, not backward-compat aliases; bare `pig update` means the same.
func isSelfUpdateTarget(source string) bool {
	switch strings.TrimSpace(strings.ToLower(source)) {
	case "self", codingagent.AppName:
		return true
	default:
		return false
	}
}

func parsePackageCommand(args []string) (*packageCLIOptions, bool) {
	if len(args) == 0 {
		return nil, false
	}
	cmd := args[0]
	if cmd == "config" {
		return nil, false
	}
	if cmd != "install" && cmd != "remove" && cmd != "update" && cmd != "list" {
		return nil, false
	}
	opts := &packageCLIOptions{command: packageCommand(cmd)}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			opts.help = true
		case arg == "-l" || arg == "--local":
			if opts.command == packageInstall || opts.command == packageRemove {
				opts.local = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case arg == "--validate-only" || arg == "--check":
			// pig additive (D28): validate-only install mode; see
			// docs/additive-features.md.
			if opts.command == packageInstall {
				opts.validateOnly = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case arg == "--json":
			if opts.command == packageInstall {
				opts.jsonOutput = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case arg == "--all":
			if opts.command == packageUpdate {
				opts.allPackages = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case arg == "--extensions":
			if opts.command == packageUpdate {
				opts.extensionsOnly = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case arg == "--self":
			if opts.command == packageUpdate {
				opts.selfOnly = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case arg == "--force":
			if opts.command == packageUpdate {
				opts.force = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case arg == "--extension":
			if opts.command != packageUpdate {
				if opts.invalidOption == "" {
					opts.invalidOption = arg
				}
				continue
			}
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				if opts.missingValue == "" {
					opts.missingValue = arg
				}
				continue
			}
			i++
			if opts.extensionSource != "" {
				opts.conflict = "--extension can only be provided once"
				continue
			}
			opts.extensionSource = args[i]
		case arg == "--set":
			if opts.command != packageInstall {
				if opts.invalidOption == "" {
					opts.invalidOption = arg
				}
				continue
			}
			if i+1 >= len(args) {
				if opts.invalidOption == "" {
					opts.invalidOption = arg
				}
				continue
			}
			i++
			opts.sources = append(opts.sources, splitInstallSetSources(args[i])...)
		case strings.HasPrefix(arg, "--set="):
			if opts.command == packageInstall {
				opts.sources = append(opts.sources, splitInstallSetSources(strings.TrimPrefix(arg, "--set="))...)
			} else if opts.invalidOption == "" {
				opts.invalidOption = "--set"
			}
		default:
			if strings.HasPrefix(arg, "-") {
				if opts.invalidOption == "" {
					opts.invalidOption = arg
				}
				continue
			}
			opts.sources = append(opts.sources, arg)
		}
	}
	opts.sources = compactStrings(opts.sources)
	if len(opts.sources) > 0 {
		opts.source = opts.sources[0]
	}
	if opts.command == packageUpdate {
		sourceIsSelf := isSelfUpdateTarget(opts.source)
		switch {
		case opts.allPackages && (opts.selfOnly || opts.extensionsOnly || opts.extensionSource != "" || opts.source != ""):
			opts.conflict = "--all cannot be combined with --self, --extensions, --extension, or a positional source"
		case opts.extensionSource != "" && (opts.selfOnly || opts.extensionsOnly || opts.source != ""):
			opts.conflict = "--extension cannot be combined with --self, --extensions, or a positional source"
		case opts.source != "" && !sourceIsSelf && (opts.selfOnly || opts.extensionsOnly):
			opts.conflict = "positional update targets cannot be combined with --self or --extensions"
		case opts.selfOnly && opts.extensionsOnly:
			opts.allPackages = true
			opts.selfOnly = false
			opts.extensionsOnly = false
		case sourceIsSelf && opts.extensionsOnly:
			opts.source = ""
			opts.allPackages = true
			opts.extensionsOnly = false
		}
		if opts.extensionSource != "" && opts.conflict == "" {
			opts.source = opts.extensionSource
		}
	}
	return opts, true
}

func packageUsage(cmd packageCommand) string {
	switch cmd {
	case packageInstall:
		return "pig install <source>... [-l] [--validate-only] [--json] [--set <sources>]"
	case packageRemove:
		return "pig remove <source> [-l]"
	case packageUpdate:
		return "pig update [source|self|pig] [--self|--extensions|--all] [--extension <source>] [--force]"
	case packageList:
		return "pig list"
	default:
		return "pig <package-command>"
	}
}

func printPackageCommandHelp(cmd packageCommand) {
	switch cmd {
	case packageInstall:
		fmt.Print("Usage:\n  pig install <source> [-l]\n  pig install --validate-only [--json] <source>...\n  pig install --validate-only [--json] --set <source[,source...]>\n\nInstall a package and add it to settings. With --validate-only, validate/build/start one or more packages without installing them.\n\nOptions:\n  -l, --local        Install project-locally (.pig/settings.json)\n  --validate-only    Validate/build/start package refs without installing them\n  --json             Emit JSON validation output with --validate-only\n  --set              Validate a comma- or whitespace-separated extension set\n\nExamples:\n  pig install npm:@foo/bar\n  pig install git:github.com/user/repo\n  pig install git:git@github.com:user/repo\n  pig install https://github.com/user/repo\n  pig install ssh://git@github.com/user/repo\n  pig install ./local/path\n  pig install ./local/path --validate-only --json\n  pig install --validate-only --json ./ext-a ./ext-b\n")
	case packageRemove:
		fmt.Print("Usage:\n  pig remove <source> [-l]\n\nRemove a package and its source from settings.\n\nOptions:\n  -l, --local    Remove from project settings (.pig/settings.json)\n\nExamples:\n  pig remove npm:@foo/bar\n")
	case packageUpdate:
		fmt.Print("Usage:\n  pig update                         Update pig itself\n  pig update self|pig                Update pig itself\n  pig update <source>                Update one installed package\n  pig update --extension <source>    Update one installed package\n  pig update --extensions            Update every installed package (not pig)\n  pig update --all                   Update packages, then pig\n\nOptions:\n  --self                  Update pig only\n  --extensions            Update installed packages only\n  --all                   Update packages, then pig\n  --extension <source>    Update one installed package only\n  --force                 Reinstall pig even when its version is current\n\nBare `pig update` self-updates the pig binary, mirroring upstream `pi update`.\n")
	case packageList:
		fmt.Print("Usage:\n  pig list\n\nList installed packages from user and project settings.\n")
	}
}

func packageContext() (cwd, agentDir string, sm *codingagent.SettingsManager, err error) {
	cwd, err = os.Getwd()
	if err != nil {
		return "", "", nil, err
	}
	agentDir = codingagent.AgentDir()
	return cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), nil
}

func reportSettingsErrors(sm *codingagent.SettingsManager, context string) {
	for _, serr := range sm.DrainErrors() {
		fmt.Fprintf(os.Stderr, "Warning (%s, %s settings): %v\n", context, serr.Scope, serr.Error)
	}
}

func init() {
	// pig additive (D18): Piglets materialize Package/direct sources through
	// the core installer without mutating Package settings.
	installresolver.SetMaterializer(func(cwd, source, scope string, stdout, _ io.Writer) (string, error) {
		local := scope == "project"
		if scope != "user" && scope != "project" {
			return "", fmt.Errorf("unknown package materialization scope %q", scope)
		}
		source = strings.TrimSpace(source)
		kind := detectSourceKind(source)
		switch kind {
		case "local":
			root, err := resolveInputPackageSourceRoot(cwd, source)
			if err != nil {
				return "", err
			}
			if _, err := os.Stat(root); err != nil {
				return "", fmt.Errorf("path does not exist: %s", root)
			}
			return root, nil
		case "npm":
			_, _ = fmt.Fprintf(stdout, "[%s] fetching %s\n", scope, source)
			if err := installManagedNPM(cwd, source, local); err != nil {
				return "", err
			}
		case "git":
			_, _ = fmt.Fprintf(stdout, "[%s] fetching %s\n", scope, source)
			if err := installManagedGit(cwd, source, local); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("unsupported package source: %s", source)
		}
		return sourceRootForResources(cwd, source, local)
	})
	installresolver.SetInstaller(func(cwd, source, scope string, stdout, stderr io.Writer) error {
		sm := codingagent.NewSettingsManager(cwd, codingagent.AgentDir())
		local := scope == "project"
		return installAndPersistPackage(cwd, sm, source, local, func(step string) {
			_, _ = fmt.Fprintln(stdout, step)
		})
	})
}

func runPackageCommand(args []string) int {
	if len(args) > 0 && args[0] == "package" {
		return runPackageManagementCommand(args[1:])
	}
	opts, ok := parsePackageCommand(args)
	if !ok {
		return -1
	}
	if opts.help {
		printPackageCommandHelp(opts.command)
		return 0
	}
	if opts.invalidOption != "" {
		fmt.Fprintf(os.Stderr, "Unknown option %s for %q.\n", opts.invalidOption, opts.command)
		fmt.Fprintf(os.Stderr, "Use %q or %q.\n", "pig --help", packageUsage(opts.command))
		return 1
	}
	if opts.missingValue != "" {
		fmt.Fprintf(os.Stderr, "Missing value for %s.\n", opts.missingValue)
		fmt.Fprintf(os.Stderr, "Usage: %s\n", packageUsage(opts.command))
		return 1
	}
	if opts.conflict != "" {
		fmt.Fprintln(os.Stderr, opts.conflict)
		fmt.Fprintf(os.Stderr, "Usage: %s\n", packageUsage(opts.command))
		return 1
	}
	if (opts.command == packageInstall || opts.command == packageRemove) && opts.source == "" {
		fmt.Fprintf(os.Stderr, "Missing %s source.\n", opts.command)
		fmt.Fprintf(os.Stderr, "Usage: %s\n", packageUsage(opts.command))
		return 1
	}
	if opts.command == packageInstall && len(opts.sources) > 1 && !opts.validateOnly {
		fmt.Fprintln(os.Stderr, "Multiple install sources require --validate-only.")
		fmt.Fprintf(os.Stderr, "Usage: %s\n", packageUsage(opts.command))
		return 1
	}
	if (opts.command == packageRemove || opts.command == packageUpdate) && len(opts.sources) > 1 {
		fmt.Fprintf(os.Stderr, "%s accepts at most one source.\n", opts.command)
		fmt.Fprintf(os.Stderr, "Usage: %s\n", packageUsage(opts.command))
		return 1
	}

	cwd, _, sm, err := packageContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	reportSettingsErrors(sm, "package command")

	if opts.command == packageUpdate {
		// pig divergence (D39): route bare update to the proven binary owner.
		return runUpdateCommand(cwd, sm, opts)
	}

	switch opts.command {
	case packageInstall:
		installSources := append([]string(nil), opts.sources...)
		if opts.validateOnly {
			// pig additive (D28): emit the validation report instead of installing.
			return validateInstallSources(cwd, installSources, opts.jsonOutput)
		}
		installSource := installSources[0]
		fmt.Printf("Installing %s...\n", opts.source)
		if err := installAndPersistPackage(cwd, sm, installSource, opts.local, nil); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Printf("Installed %s\n", opts.source)
		return 0
	case packageRemove:
		removed, err := removeAndPersistPackage(cwd, sm, opts.source, opts.local)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		if !removed {
			fmt.Fprintf(os.Stderr, "No matching package found for %s\n", opts.source)
			return 1
		}
		fmt.Printf("Removed %s\n", opts.source)
		return 0
	case packageList:
		return listPackages(cwd, sm)
	default:
		return 1
	}
}

// runUpdateCommand routes `pig update`. Bare or a self target self-updates the
// binary (D39); `<source>` updates one package; `--all` refreshes packages and
// then self-updates, matching upstream's observable operation order.
func runUpdateCommand(cwd string, sm *codingagent.SettingsManager, opts *packageCLIOptions) int {
	if opts.source != "" && !isSelfUpdateTarget(opts.source) {
		if err := updatePackages(cwd, sm, opts.source, func(step string) { fmt.Fprintln(os.Stderr, step) }); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Printf("Updated %s\n", opts.source)
		return 0
	}

	// --extensions updates every package without touching pig itself.
	if opts.extensionsOnly && !opts.allPackages {
		if err := updatePackages(cwd, sm, "", func(step string) { fmt.Fprintln(os.Stderr, step) }); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Println("Updated packages")
		return 0
	}

	if !opts.allPackages {
		return runSelfUpdate(opts.force)
	}
	if err := updatePackages(cwd, sm, "", func(step string) { fmt.Fprintln(os.Stderr, step) }); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Println("Updated packages")
	return runSelfUpdate(opts.force)
}

func installAndPersistPackage(cwd string, sm *codingagent.SettingsManager, source string, local bool, progress ProgressCallback) error {
	pkg := codingagent.PackageSource{Source: source}
	if err := installPackageArtifacts(cwd, pkg, local, progress); err != nil {
		return err
	}
	if err := verifyPackageContributesResources(cwd, source, local); err != nil {
		return err
	}
	if err := addSourceToSettings(cwd, sm, source, local); err != nil {
		return err
	}
	return nil
}

// pig divergence (D57): reject a proven extension root that Package discovery
// cannot load.
func verifyPackageContributesResources(cwd, source string, local bool) error {
	root := installedPathForSource(cwd, source, local)
	if root == "" {
		// The source resolved to no local root (a remote form this build cannot
		// inspect). Nothing to assert against, so stay out of the way.
		return nil
	}
	resources, err := packagecontent.Discover(root)
	if err != nil {
		return fmt.Errorf("inspect installed package %s: %w", source, err)
	}
	if packageResourceCount(resources) > 0 {
		return nil
	}
	// Refuse only a complete extension contract. Empty Packages remain valid.
	if !directoryIsProvablyAnExtension(root) {
		return nil
	}
	return fmt.Errorf("%s is an extension, not a package, so installing it as one loads nothing.\n"+
		"Load it directly with `pig -e %s`, put it in ~/.pig/agent/extensions/, or publish it inside a "+
		"package's extensions/ directory", source, source)
}

// directoryIsProvablyAnExtension accepts only a complete conventional factory
// or standalone contract. A build marker alone never promotes a Package root.
func directoryIsProvablyAnExtension(root string) bool {
	_, err := extsource.Resolve(root)
	return err == nil
}

func packageResourceCount(r packagecontent.Resources) int {
	return len(r.ExtensionEntries) + len(r.SkillDirs) + len(r.PromptFiles) + len(r.ThemeFiles) +
		len(r.AgentFiles) + len(r.MCPFiles) + len(r.HookFiles) + len(r.AgentEnvironments)
}

func getGitDependencyInstallArgs(sm *codingagent.SettingsManager) []string {
	configuredCommand := sm.GetNpmCommand()
	if len(configuredCommand) > 0 {
		return []string{"install"}
	}
	return []string{"install", "--omit=dev"}
}

func removeAndPersistPackage(cwd string, sm *codingagent.SettingsManager, source string, local bool) (bool, error) {
	removed, err := removeSourceFromSettings(cwd, sm, source, local)
	if err != nil || !removed {
		return removed, err
	}
	if err := removePackageArtifacts(cwd, sm, source, local); err != nil {
		return false, err
	}
	return true, nil
}

func updatePackages(cwd string, sm *codingagent.SettingsManager, source string, progress ProgressCallback) error {
	pkgs := listConfiguredPackages(cwd, sm)
	if source != "" {
		identity := packageIdentityFromInput(cwd, source)
		for _, pkg := range pkgs {
			if packageIdentityFromStored(cwd, pkg.Source.Source, pkg.Scope == "project") == identity {
				local := pkg.Scope == "project"
				if err := installPackageArtifacts(cwd, pkg.Source, local, progress); err != nil {
					return err
				}
				return nil
			}
		}
		return errors.New(noMatchingPackageMessage(cwd, source, pkgs))
	}
	for _, pkg := range pkgs {
		local := pkg.Scope == "project"
		if err := installPackageArtifacts(cwd, pkg.Source, local, progress); err != nil {
			return err
		}
	}
	return nil
}

type gitPackageSource struct {
	host   string
	path   string
	ref    string
	pinned bool
}

func settingsBaseDirForScope(cwd string, local bool) string {
	if local {
		return codingagent.ProjectConfigDir(cwd)
	}
	return codingagent.AgentDir()
}

func settingsBaseDirForManager(sm *codingagent.SettingsManager, local bool) string {
	if local {
		return codingagent.ProjectConfigDir(sm.CWD())
	}
	return sm.AgentDir()
}

// normalizePackageSourceForSettings stores local package paths relative to the
// settings directory, matching upstream.
func normalizePackageSourceForSettings(baseDir, cwd, source string) string {
	kind := detectSourceKind(source)
	if kind == "npm" && !strings.HasPrefix(strings.TrimSpace(source), "npm:") {
		return "npm:" + strings.TrimSpace(source)
	}
	if kind != "local" {
		return source
	}
	resolved := resolveInputLocalPackageRoot(cwd, source)
	rel, err := filepath.Rel(baseDir, resolved)
	if err != nil {
		return source
	}
	return filepath.Clean(rel)
}

func addSourceToSettings(cwd string, sm *codingagent.SettingsManager, source string, local bool) error {
	normalized := normalizePackageSourceForSettings(settingsBaseDirForManager(sm, local), cwd, source)
	pkg := codingagent.PackageSource{Source: normalized}
	if local {
		current := append([]codingagent.PackageSource{}, sm.GetProjectSettings().Packages...)
		baseDir := settingsBaseDirForManager(sm, true)
		if slices.ContainsFunc(current, func(existing codingagent.PackageSource) bool {
			return packageMatchKeyForStoredBase(baseDir, existing.Source) == packageMatchKeyForInput(cwd, source)
		}) {
			return nil
		}
		current = append(current, pkg)
		return sm.SetProjectPackages(current)
	}
	current := append([]codingagent.PackageSource{}, sm.GetGlobalSettings().Packages...)
	baseDir := settingsBaseDirForManager(sm, false)
	if slices.ContainsFunc(current, func(existing codingagent.PackageSource) bool {
		return packageMatchKeyForStoredBase(baseDir, existing.Source) == packageMatchKeyForInput(cwd, source)
	}) {
		return nil
	}
	current = append(current, pkg)
	return sm.SetPackages(current)
}

func removeSourceFromSettings(cwd string, sm *codingagent.SettingsManager, source string, local bool) (bool, error) {
	if local {
		current := sm.GetProjectSettings().Packages
		next := make([]codingagent.PackageSource, 0, len(current))
		removed := false
		baseDir := settingsBaseDirForManager(sm, true)
		for _, pkg := range current {
			if packageMatchKeyForStoredBase(baseDir, pkg.Source) == packageMatchKeyForInput(cwd, source) {
				removed = true
				continue
			}
			next = append(next, pkg)
		}
		if !removed {
			return false, nil
		}
		return true, sm.SetProjectPackages(next)
	}
	current := sm.GetGlobalSettings().Packages
	next := make([]codingagent.PackageSource, 0, len(current))
	removed := false
	baseDir := settingsBaseDirForManager(sm, false)
	for _, pkg := range current {
		if packageMatchKeyForStoredBase(baseDir, pkg.Source) == packageMatchKeyForInput(cwd, source) {
			removed = true
			continue
		}
		next = append(next, pkg)
	}
	if !removed {
		return false, nil
	}
	return true, sm.SetPackages(next)
}

func listPackages(cwd string, sm *codingagent.SettingsManager) int {
	pkgs := listConfiguredPackages(cwd, sm)
	if len(pkgs) == 0 {
		fmt.Println("No packages installed.")
		return 0
	}
	userPkgs := make([]configuredPackage, 0)
	projectPkgs := make([]configuredPackage, 0)
	for _, pkg := range pkgs {
		if pkg.Scope == "project" {
			projectPkgs = append(projectPkgs, pkg)
		} else {
			userPkgs = append(userPkgs, pkg)
		}
	}
	format := func(title string, pkgs []configuredPackage) {
		if len(pkgs) == 0 {
			return
		}
		fmt.Println(title)
		for _, pkg := range pkgs {
			display := pkg.Source.Source
			if pkg.Source.Filtered() {
				display += " (filtered)"
			}
			fmt.Printf("  %s\n", display)
			if pkg.InstalledPath != "" {
				fmt.Printf("    %s\n", pkg.InstalledPath)
			}
		}
	}
	format("User packages:", userPkgs)
	if len(userPkgs) > 0 && len(projectPkgs) > 0 {
		fmt.Println()
	}
	format("Project packages:", projectPkgs)
	return 0
}

func listConfiguredPackages(cwd string, sm *codingagent.SettingsManager) []configuredPackage {
	global := sm.GetGlobalSettings().Packages
	project := sm.GetProjectSettings().Packages
	packages := make([]configuredPackage, 0, len(global)+len(project))
	for _, pkg := range global {
		packages = append(packages, configuredPackage{Source: pkg, Scope: "user", InstalledPath: installedPathForConfiguredSource(cwd, sm, pkg.Source, false)})
	}
	for _, pkg := range project {
		packages = append(packages, configuredPackage{Source: pkg, Scope: "project", InstalledPath: installedPathForConfiguredSource(cwd, sm, pkg.Source, true)})
	}
	return packages
}

func emitProgress(cb ProgressCallback, format string, args ...any) {
	if cb != nil {
		cb(fmt.Sprintf(format, args...))
	}
}

func installPackageArtifacts(cwd string, pkg codingagent.PackageSource, local bool, progress ProgressCallback) error {
	kind := detectSourceKind(pkg.Source)
	if kind == "local" {
		emitProgress(progress, "[%s] validating %s", packageScopeName(local), pkg.Source)
		root, err := resolveInputPackageSourceRoot(cwd, pkg.Source)
		if err != nil {
			return err
		}
		if _, err := os.Stat(root); err != nil {
			return fmt.Errorf("path does not exist: %s", root)
		}
		emitProgress(progress, "[%s] ready %s", packageScopeName(local), pkg.Source)
		return nil
	}
	emitProgress(progress, "[%s] fetching %s", packageScopeName(local), pkg.Source)
	switch kind {
	case "npm":
		if err := installManagedNPM(cwd, pkg.Source, local); err != nil {
			return err
		}
	case "git":
		if err := installManagedGit(cwd, pkg.Source, local); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported package source: %s", pkg.Source)
	}
	installedPath := installedPathForSource(cwd, pkg.Source, local)
	if installedPath == "" {
		return fmt.Errorf("installed package path not found for %s", pkg.Source)
	}
	emitProgress(progress, "[%s] installed %s", packageScopeName(local), pkg.Source)
	return nil
}

func removePackageArtifacts(cwd string, sm *codingagent.SettingsManager, source string, local bool) error {
	switch detectSourceKind(source) {
	case "npm":
		return uninstallManagedNPM(cwd, source, local)
	case "git":
		checkout, err := gitCheckoutPath(cwd, source, local)
		if err != nil {
			return err
		}
		if configuredGitCheckoutInUse(cwd, sm, source, local) {
			return nil
		}
		return os.RemoveAll(checkout)
	default:
		return nil
	}
}

func configuredGitCheckoutInUse(cwd string, sm *codingagent.SettingsManager, removedSource string, local bool) bool {
	removed, err := sourceref.Parse(removedSource, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || removed.Kind != sourceref.KindGit {
		return false
	}
	wantScope := "user"
	if local {
		wantScope = "project"
	}
	for _, pkg := range listConfiguredPackages(cwd, sm) {
		if pkg.Scope != wantScope {
			continue
		}
		ref, err := sourceref.Parse(pkg.Source.Source, sourceref.Options{Bare: sourceref.BareReject})
		if err == nil && ref.Kind == sourceref.KindGit && ref.GitHost == removed.GitHost && ref.GitPath == removed.GitPath {
			return true
		}
	}
	return false
}

func sourceRootForResources(cwd, source string, local bool) (string, error) {
	switch detectSourceKind(source) {
	case "npm":
		ref, err := parseNpmInstallRef(source)
		if err != nil {
			return "", err
		}
		return npmInstallPath(cwd, ref, local), nil
	case "git":
		return gitInstallPath(cwd, source, local)
	case "local":
		baseDir := settingsBaseDirForScope(cwd, local)
		return resolveLocalPackageRoot(baseDir, source)
	default:
		return "", fmt.Errorf("unsupported package source: %s", source)
	}
}

func resolveInputPackageSourceRoot(cwd, source string) (string, error) {
	if detectSourceKind(source) != "local" {
		return sourceRootForResources(cwd, source, false)
	}
	return resolveLocalPackageRoot(cwd, source)
}

func resolveInputLocalPackageRoot(cwd, source string) string {
	root, _ := resolveLocalPackageRoot(cwd, source)
	return root
}

func resolveLocalPackageRoot(baseDir, source string) (string, error) {
	if filepath.IsAbs(source) {
		return filepath.Clean(source), nil
	}
	return filepath.Abs(filepath.Join(baseDir, source))
}

func npmInstallPath(cwd string, ref sourceref.Ref, local bool) string {
	installRoot := npmInstallRoot(cwd, ref, local)
	managedPath := filepath.Join(installRoot, "node_modules", filepath.FromSlash(ref.NPMName))
	if local || ref.NPMRegistry != "" {
		return managedPath
	}
	if _, err := os.Stat(managedPath); err == nil {
		return managedPath
	}
	if pnpmPath := getPnpmGlobalPackagePath(cwd, ref.NPMName); pnpmPath != "" {
		if _, err := os.Stat(pnpmPath); err == nil {
			return pnpmPath
		}
	}
	return managedPath
}

func npmInstallRoot(cwd string, ref sourceref.Ref, local bool) string {
	root := codingagent.NPMInstallRoot(cwd, codingagent.AgentDir(), local)
	if ref.NPMRegistry == "" {
		return root
	}
	digest := sha256.Sum256([]byte(ref.NPMRegistry))
	return filepath.Join(root, "registries", fmt.Sprintf("%x", digest[:8]))
}

func getPnpmGlobalPackagePath(cwd, packageName string) string {
	npmCommand := defaultNpmCommand(cwd)
	packageManagerName := npmCommandName(npmCommand)
	if packageManagerName != "pnpm" {
		return ""
	}
	output, err := runCmd(npmCommand[0], append(npmCommand[1:], "list", "-g", "--depth", "0", "--json")...)
	if err != nil {
		return ""
	}
	var entries []struct {
		Dependencies map[string]struct {
			Path string `json:"path"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(output), &entries); err == nil {
		for _, entry := range entries {
			if dep, ok := entry.Dependencies[packageName]; ok && dep.Path != "" {
				return dep.Path
			}
		}
	}
	var single struct {
		Dependencies map[string]struct {
			Path string `json:"path"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(output), &single); err != nil {
		return ""
	}
	if dep, ok := single.Dependencies[packageName]; ok {
		return dep.Path
	}
	return ""
}

func defaultNpmCommand(cwd string) []string {
	sm := codingagent.NewSettingsManager(cwd, codingagent.AgentDir())
	if cmd := sm.GetNpmCommand(); len(cmd) > 0 {
		return append([]string(nil), cmd...)
	}
	return []string{"npm"}
}

func npmCommandName(cmd []string) string {
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
	if idx >= 0 && idx+1 < len(cmd) {
		target = cmd[idx+1]
	}
	base := filepath.Base(target)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	name := strings.ToLower(base)
	if name == "npm" && len(cmd) == 1 {
		if out, err := runCmd(target, "--version"); err == nil {
			if strings.Contains(strings.ToLower(out), "pnpm") {
				return "pnpm"
			}
		}
	}
	return name
}

func installedPathForSource(cwd, source string, local bool) string {
	path, err := sourceRootForResources(cwd, source, local)
	if err == nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return path
		}
	}
	return ""
}

func packageMatchKeyForStoredBase(baseDir, source string) string {
	return packageSourceIdentity(baseDir, source)
}

func packageMatchKeyForInput(cwd, source string) string {
	return packageMatchKey(cwd, source, func() string { return cwd })
}

func packageMatchKey(cwd, source string, localBaseDir func() string) string {
	baseDir := localBaseDir()
	if baseDir == "" {
		baseDir = cwd
	}
	return packageSourceIdentity(baseDir, source)
}

func packageIdentityFromInput(cwd, source string) string {
	return packageSourceIdentity(cwd, source)
}

func packageIdentityFromStored(cwd, source string, local bool) string {
	return packageSourceIdentity(settingsBaseDirForScope(cwd, local), source)
}

// packageSourceIdentity centralizes upstream package identity and Pig contributed
// scheme identity. Identity matching follows upstream parseSource: an unprefixed
// value is local; bare-name-as-npm is only an install-boundary convenience.
//
// pig additive (D18): registered contributed source schemes participate in
// the same deterministic identity contract as upstream npm/git/local sources.
func packageSourceIdentity(baseDir, raw string) string {
	ref, err := sourceref.Parse(raw, sourceref.Options{
		BaseDir:          baseDir,
		Bare:             sourceref.BareLocal,
		AllowContributed: true,
	})
	if err != nil {
		return "unsupported:" + strings.TrimSpace(raw)
	}
	identity, err := ref.Identity(baseDir)
	if err != nil {
		return "unsupported:" + strings.TrimSpace(raw)
	}
	return identity
}

func noMatchingPackageMessage(cwd, source string, pkgs []configuredPackage) string {
	if suggestion := findSuggestedConfiguredSource(source, pkgs); suggestion != "" {
		return fmt.Sprintf("No matching package found for %s. Did you mean %s?", source, suggestion)
	}
	return fmt.Sprintf("No matching package found for %s", source)
}

func findSuggestedConfiguredSource(source string, pkgs []configuredPackage) string {
	trimmed := strings.TrimSpace(source)
	for _, pkg := range pkgs {
		sourceStr := pkg.Source.Source
		if name, spec, ok := parseNpmSource(sourceStr); ok {
			if trimmed == name || trimmed == spec {
				return sourceStr
			}
			continue
		}
		if git, ok := parseGitPackageSource(sourceStr); ok {
			shorthand := git.host + "/" + git.path
			if trimmed == shorthand {
				return sourceStr
			}
			if git.ref != "" && trimmed == shorthand+"@"+git.ref {
				return sourceStr
			}
		}
	}
	return ""
}

func parseNpmSource(source string) (name, spec string, ok bool) {
	if !strings.HasPrefix(source, "npm:") {
		return "", "", false
	}
	spec = strings.TrimSpace(strings.TrimPrefix(source, "npm:"))
	name, _ = parseNpmSpec(spec)
	return name, spec, name != ""
}

func parseNpmInstallRef(source string) (sourceref.Ref, error) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareNPM})
	if err != nil || ref.Kind != sourceref.KindNPM {
		return sourceref.Ref{}, fmt.Errorf("invalid npm package source: %s", source)
	}
	return ref, nil
}

func parseGitPackageSource(source string) (gitPackageSource, bool) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return gitPackageSource{}, false
	}
	return gitPackageSource{
		host: ref.GitHost, path: ref.GitPath, ref: ref.GitRef, pinned: ref.GitRef != "",
	}, true
}

func installManagedNPM(cwd, source string, local bool) error {
	ref, err := parseNpmInstallRef(source)
	if err != nil {
		return err
	}
	installRoot := npmInstallRoot(cwd, ref, local)
	if err := ensureManagedPackageRoot(installRoot); err != nil {
		return err
	}
	command := defaultNpmCommand(cwd)
	args := append([]string{}, command[1:]...)
	args = append(args, npmInstallArgs(npmCommandName(command), ref.Locator, installRoot, ref.NPMRegistry)...)
	_, err = runCmd(command[0], args...)
	return err
}

func uninstallManagedNPM(cwd, source string, local bool) error {
	ref, err := parseNpmInstallRef(source)
	if err != nil {
		return err
	}
	installRoot := npmInstallRoot(cwd, ref, local)
	if _, err := os.Stat(installRoot); os.IsNotExist(err) {
		return nil
	}
	command := defaultNpmCommand(cwd)
	args := append([]string{}, command[1:]...)
	if npmCommandName(command) == "bun" {
		args = append(args, "uninstall", ref.NPMName, "--cwd", installRoot)
	} else {
		args = append(args, "uninstall", ref.NPMName, "--prefix", installRoot)
		if npmCommandName(command) != "pnpm" {
			args = append(args, "--legacy-peer-deps")
		}
	}
	if ref.NPMRegistry != "" {
		args = append(args, "--registry", ref.NPMRegistry)
	}
	_, err = runCmd(command[0], args...)
	return err
}

func npmInstallArgs(manager, spec, installRoot, registry string) []string {
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

func ensureManagedPackageRoot(root string) error {
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

func installManagedGit(cwd, source string, local bool) error {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return fmt.Errorf("invalid Git package source: %s", source)
	}
	checkout, err := gitCheckoutPath(cwd, source, local)
	if err != nil {
		return err
	}
	root := codingagent.GitInstallRoot(cwd, codingagent.AgentDir(), local)
	if err := ensureManagedCheckoutRoot(root); err != nil {
		return err
	}
	if _, err := os.Stat(checkout); os.IsNotExist(err) {
		repo := ref.GitRepo
		if !strings.Contains(repo, "://") && !strings.HasPrefix(repo, "git@") {
			repo = "https://" + repo
		}
		if _, err := runCmd("git", "clone", gitCloneRepo(runtime.GOOS, repo), checkout); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if ref.GitRef == "" {
		if _, err := runCmd("git", "-C", checkout, "pull", "--ff-only"); err != nil {
			return err
		}
	}
	if ref.GitRef != "" {
		if _, err := runCmd("git", "-C", checkout, "fetch", "origin", ref.GitRef); err != nil {
			return err
		}
		if _, err := runCmd("git", "-C", checkout, "checkout", "FETCH_HEAD"); err != nil {
			return err
		}
	}
	packageRoot, err := gitInstallPath(cwd, source, local)
	if err != nil {
		return err
	}
	info, err := os.Stat(packageRoot)
	if err != nil {
		return fmt.Errorf("Git package subdirectory %q does not exist in %s: %w", ref.GitSubdir, ref.GitRepo, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("Git package subdirectory %q in %s is not a directory", ref.GitSubdir, ref.GitRepo)
	}
	if err := requireGitSubdirectoryWithinCheckout(checkout, packageRoot); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(packageRoot, "package.json")); err == nil {
		command := defaultNpmCommand(cwd)
		args := append([]string{}, command[1:]...)
		args = append(args, getGitDependencyInstallArgs(codingagent.NewSettingsManager(cwd, codingagent.AgentDir()))...)
		if _, err := runCmdInDir(packageRoot, command[0], args...); err != nil {
			return err
		}
	}
	return nil
}

func gitCheckoutPath(cwd, source string, local bool) (string, error) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return "", fmt.Errorf("invalid Git package source: %s", source)
	}
	root := codingagent.GitInstallRoot(cwd, codingagent.AgentDir(), local)
	relative, err := gitCheckoutRelative(runtime.GOOS, root, ref)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, relative), nil
}

// gitCheckoutRelative is a Git source's checkout directory below the install
// root: its host, then its repository path. Windows cannot name a directory
// with ':', so there a file:// source's leading drive (C:) becomes the segment
// C, and any other segment containing ':' is refused, since NTFS reads
// name:stream as an alternate data stream.
func gitCheckoutRelative(goos, root string, ref sourceref.Ref) (string, error) {
	segments := append([]string{ref.GitHost}, strings.Split(ref.GitPath, "/")...)
	if goos == "windows" {
		// pig additive (D18): a Windows file URL's drive is a checkout segment.
		if strings.HasPrefix(strings.ToLower(ref.GitRepo), "file://") && isDriveSegment(segments[1]) {
			segments[1] = segments[1][:1]
		}
		for _, segment := range segments {
			if strings.Contains(segment, ":") {
				return "", fmt.Errorf("Refusing to use path outside package install root: %s", filepath.Join(append([]string{root}, segments...)...))
			}
		}
	}
	relative := filepath.Clean(filepath.Join(segments...))
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("invalid Git package path: %s", ref.GitPath)
	}
	return relative, nil
}

// gitCloneRepo is the URL git clones for repo. Git for Windows reads
// file://localhost/C:/... as the UNC path //localhost/C:/..., so there a
// localhost file URL with a drive becomes the equivalent file:///C:/....
func gitCloneRepo(goos, repo string) string {
	const localhost = "file://localhost/"
	if goos != "windows" || len(repo) < len(localhost) || !strings.EqualFold(repo[:len(localhost)], localhost) {
		return repo
	}
	rest := repo[len(localhost):]
	drive, _, _ := strings.Cut(rest, "/")
	if !isDriveSegment(drive) {
		return repo
	}
	return "file:///" + rest
}

// isDriveSegment reports whether segment is a Windows drive such as C:.
func isDriveSegment(segment string) bool {
	return len(segment) == 2 && segment[1] == ':' && ('A' <= segment[0] && segment[0] <= 'Z' || 'a' <= segment[0] && segment[0] <= 'z')
}

func gitInstallPath(cwd, source string, local bool) (string, error) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return "", fmt.Errorf("invalid Git package source: %s", source)
	}
	checkout, err := gitCheckoutPath(cwd, source, local)
	if err != nil {
		return "", err
	}
	if ref.GitSubdir == "" {
		return checkout, nil
	}
	return filepath.Join(checkout, filepath.FromSlash(ref.GitSubdir)), nil
}

func requireGitSubdirectoryWithinCheckout(checkout, packageRoot string) error {
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

func ensureManagedCheckoutRoot(root string) error {
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

func detectSourceKind(source string) string {
	// Package install preserves upstream's bare-name-as-npm behavior, while an
	// existing bare filesystem entry remains local. Explicit contributed schemes
	// are accepted only when a resolver has claimed them.
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareNPM, AllowContributed: true})
	if err != nil {
		return "unsupported"
	}
	if ref.Kind == sourceref.KindNPM {
		if _, statErr := os.Stat(strings.TrimSpace(source)); statErr == nil {
			ref, err = sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareLocal})
			if err != nil {
				return "unsupported"
			}
		}
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

func runCmd(name string, args ...string) (string, error) {
	return runCmdInDir("", name, args...)
}

// runCmdInDir runs a package-manager or git command as upstream's
// spawnProcess does, so a Windows .cmd shim such as npm.cmd receives its
// arguments exactly.
func runCmdInDir(dir, name string, args ...string) (string, error) {
	cmd := crossspawn.Command(context.Background(), name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
