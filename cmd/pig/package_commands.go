package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

type packageCommand string

const (
	packageInstall packageCommand = "install"
	packageRemove  packageCommand = "remove"
	packageUpdate  packageCommand = "update"
	packageList    packageCommand = "list"
)

type packageCLIOptions struct {
	command              packageCommand
	source               string
	sources              []string
	local                bool
	projectTrustOverride *bool
	allPackages          bool // pig update --all: self-update plus every configured package
	extensionsOnly       bool // pig update --extensions: update every package, not pig itself
	selfOnly             bool
	modelsOnly           bool
	extensionSource      string
	force                bool
	validateOnly         bool
	jsonOutput           bool
	help                 bool
	invalidOption        string
	missingValue         string
	invalidArgument      string
	conflict             string
	// showExtensionsSkippedNote marks a bare `pig update`, which updates pig only.
	showExtensionsSkippedNote bool
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
	if cmd == "uninstall" {
		cmd = "remove"
	}
	if cmd == "config" {
		return nil, false
	}
	if cmd != "install" && cmd != "remove" && cmd != "update" && cmd != "list" {
		return nil, false
	}
	opts := &packageCLIOptions{command: packageCommand(cmd)}
	var positionals []string
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
		case arg == "--approve" || arg == "-a":
			opts.projectTrustOverride = new(true)
		case arg == "--no-approve" || arg == "-na":
			opts.projectTrustOverride = new(false)
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
		case arg == "--models":
			if opts.command == packageUpdate {
				opts.modelsOnly = true
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
			if arg != "" {
				positionals = append(positionals, arg)
			}
		}
	}
	opts.sources = compactStrings(opts.sources)
	if len(opts.sources) > 0 {
		opts.source = opts.sources[0]
	}
	// The first positional is the source and the next one is unexpected.
	// pig additive (D28): --validate-only installs accept several sources.
	if len(positionals) > 1 && (opts.command != packageInstall || !opts.validateOnly) {
		opts.invalidArgument = positionals[1]
	}
	if opts.command == packageUpdate {
		// The first conflict found wins: --all, then --models, --extension, and a positional target.
		conflict := func(message string) {
			if opts.conflict == "" {
				opts.conflict = message
			}
		}
		if opts.allPackages && (opts.selfOnly || opts.extensionsOnly || opts.modelsOnly || opts.extensionSource != "") {
			conflict("--all cannot be combined with --self, --extensions, --models, or --extension")
		}
		if opts.allPackages && opts.source != "" {
			conflict("--all cannot be combined with a positional source")
		}
		switch {
		case opts.modelsOnly:
			if opts.selfOnly || opts.extensionsOnly || opts.allPackages || opts.extensionSource != "" {
				conflict("--models cannot be combined with --self, --extensions, --all, or --extension")
			}
			if opts.source != "" {
				conflict("--models cannot be combined with a positional source")
			}
		case opts.extensionSource != "":
			if opts.selfOnly || opts.extensionsOnly || opts.allPackages {
				conflict("--extension cannot be combined with --self, --extensions, or --all")
			}
			if opts.source != "" {
				conflict("--extension cannot be combined with a positional source")
			}
			if opts.conflict == "" {
				opts.source = opts.extensionSource
			}
		case opts.source != "":
			if isSelfUpdateTarget(opts.source) {
				if opts.extensionsOnly {
					opts.source = ""
					opts.allPackages = true
					opts.extensionsOnly = false
				}
			} else if opts.extensionsOnly || opts.selfOnly || opts.allPackages {
				conflict("positional update targets cannot be combined with --self, --extensions, or --all")
			}
		case opts.allPackages:
		case opts.selfOnly && opts.extensionsOnly:
			opts.allPackages = true
			opts.selfOnly = false
			opts.extensionsOnly = false
		case !opts.selfOnly && !opts.extensionsOnly:
			opts.showExtensionsSkippedNote = true
		}
	}
	return opts, true
}

func packageUsage(cmd packageCommand) string {
	switch cmd {
	case packageInstall:
		return "pig install <source> [-l] [--approve|--no-approve]"
	case packageRemove:
		return "pig remove <source> [-l] [--approve|--no-approve]"
	case packageUpdate:
		return "pig update [source|self|pig] [--self|--extensions|--models|--all] [--extension <source>] [--approve|--no-approve] [--force]"
	case packageList:
		return "pig list [--approve|--no-approve]"
	default:
		return "pig <package-command>"
	}
}

func printPackageCommandHelp(cmd packageCommand) {
	switch cmd {
	case packageInstall:
		fmt.Print("Usage:\n  pig install <source> [-l] [--approve|--no-approve]\n  pig install --validate-only [--json] <source>...\n  pig install --validate-only [--json] --set <source[,source...]>\n\nInstall a package and add it to settings. With --validate-only, validate/build/start one or more packages without installing them.\n\nOptions:\n  -l, --local        Install project-locally (.pig/settings.json)\n  -a, --approve     Trust project-local files for this command\n  -na, --no-approve Ignore project-local files for this command\n  --validate-only    Validate/build/start package refs without installing them\n  --json             Emit JSON validation output with --validate-only\n  --set              Validate a comma- or whitespace-separated extension set\n\nExamples:\n  pig install npm:@foo/bar\n  pig install git:github.com/user/repo\n  pig install git:git@github.com:user/repo\n  pig install https://github.com/user/repo\n  pig install ssh://git@github.com/user/repo\n  pig install ./local/path\n  pig install ./local/path --validate-only --json\n  pig install --validate-only --json ./ext-a ./ext-b\n")
	case packageRemove:
		fmt.Print("Usage:\n  pig remove <source> [-l] [--approve|--no-approve]\n\nRemove a package and its source from settings.\nAlias: pig uninstall <source> [-l]\n\nOptions:\n  -l, --local       Remove from project settings (.pig/settings.json)\n  -a, --approve     Trust project-local files for this command\n  -na, --no-approve Ignore project-local files for this command\n\nExamples:\n  pig remove npm:@foo/bar\n  pig uninstall npm:@foo/bar\n\n")
	case packageUpdate:
		fmt.Print("Usage:\n  " + packageUsage(packageUpdate) + "\n\nUpdate pig, installed packages, or model catalogs.\n\nOptions:\n  --self                  Update pig only (default when no target is given)\n  --extensions            Update installed packages only\n  --models                Refresh model catalogs only\n  --all                   Update pig and installed packages\n  --extension <source>    Update one package only\n  -a, --approve           Trust project-local files for this command\n  -na, --no-approve       Ignore project-local files for this command\n  --force                 Reinstall pig even if the current version is latest\n\nShort forms:\n  pig update                Update pig only\n  pig update --all          Update pig and all extensions\n  pig update --models       Refresh model catalogs only\n  pig update <source>       Update one package\n  pig update pig            Update pig only (self works as alias to pig)\n\n")
	case packageList:
		fmt.Print("Usage:\n  pig list [--approve|--no-approve]\n\nList installed packages from user and project settings.\n\nOptions:\n  -a, --approve     Trust project-local files for this command\n  -na, --no-approve Ignore project-local files for this command\n")
	}
}

func packageContext() (cwd, agentDir string, sm *codingagent.SettingsManager, err error) {
	cwd, err = os.Getwd()
	if err != nil {
		return "", "", nil, err
	}
	agentDir = codingagent.AgentDir()
	// pig additive (D40): pre-session inspection resolves saved/default trust without invoking extension handlers.
	sm = codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: cwd, Store: codingagent.NewProjectTrustStore(agentDir), Default: sm.GetGlobalSettings().DefaultProjectTrust,
	})
	if err != nil {
		return "", "", nil, err
	}
	sm.SetProjectTrusted(trusted)
	return cwd, agentDir, sm, nil
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
		sm, err := createPackageCommandSettings(context.Background(), cwd, codingagent.AgentDir(), &packageCLIOptions{command: packageInstall})
		if err != nil {
			return "", err
		}
		if local && !sm.IsProjectTrusted() {
			return "", errors.New("Project is not trusted; refusing to access project package storage")
		}
		source = strings.TrimSpace(source)
		kind := packagemanager.DetectSourceKind(source)
		switch kind {
		case "local":
			root, err := packagemanager.ResolveInputPackageSourceRoot(cwd, sm.AgentDir(), sm, source)
			if err != nil {
				return "", err
			}
			if _, err := os.Stat(root); err != nil {
				return "", fmt.Errorf("Path does not exist: %s", root)
			}
			return root, nil
		case "npm":
			_, _ = fmt.Fprintf(stdout, "[%s] fetching %s\n", scope, source)
			if err := packagemanager.InstallManagedNPM(cwd, sm.AgentDir(), sm, source, local); err != nil {
				return "", err
			}
		case "git":
			_, _ = fmt.Fprintf(stdout, "[%s] fetching %s\n", scope, source)
			if err := packagemanager.InstallManagedGit(cwd, sm.AgentDir(), sm, source, local); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("unsupported package source: %s", source)
		}
		return packagemanager.SourceRootForResources(cwd, sm.AgentDir(), sm, source, local)
	})
	installresolver.SetInstaller(func(cwd, source, scope string, stdout, stderr io.Writer) error {
		sm, err := createPackageCommandSettings(context.Background(), cwd, codingagent.AgentDir(), &packageCLIOptions{command: packageInstall})
		if err != nil {
			return err
		}
		local := scope == "project"
		return installAndPersistPackage(cwd, sm, source, local, packageProgressPrinter(stdout))
	})
}

func runPackageCommand(args []string, runtimeOptions ...packageCommandRuntimeOptions) int {
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
	if opts.invalidArgument != "" {
		fmt.Fprintf(os.Stderr, "Unexpected argument %s.\n", opts.invalidArgument)
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
	if opts.command == packageUpdate && opts.modelsOnly {
		if err := refreshModelCatalogs(codingagent.AgentDir()); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	cwd, err := os.Getwd()
	if err != nil {
		printCLIError("%v", err)
		return 1
	}
	sm, err := createPackageCommandSettings(context.Background(), cwd, codingagent.AgentDir(), opts, runtimeOptions...)
	if err != nil {
		printCLIError("%v", err)
		return 1
	}
	if opts.local && !sm.IsProjectTrusted() && (opts.command == packageInstall || opts.command == packageRemove) {
		fmt.Fprintln(os.Stderr, "Project is not trusted. Use --approve to modify local package config.")
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
			return validateInstallSources(cwd, sm, installSources, opts.jsonOutput)
		}
		installSource := installSources[0]
		if err := installAndPersistPackage(cwd, sm, installSource, opts.local, packageProgressPrinter(os.Stdout)); err != nil {
			printCLIError("%v", err)
			return 1
		}
		fmt.Printf("Installed %s\n", opts.source)
		return 0
	case packageRemove:
		fmt.Printf("Removing %s...\n", opts.source)
		removed, err := removeAndPersistPackage(cwd, sm, opts.source, opts.local)
		if err != nil {
			printCLIError("%v", err)
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
	if opts.showExtensionsSkippedNote {
		fmt.Printf("Extensions are skipped. Run %s update --extensions to update extensions.\n", codingagent.AppName)
	}
	if opts.source != "" && !isSelfUpdateTarget(opts.source) {
		if err := updatePackages(cwd, sm, opts.source, packageProgressPrinter(os.Stdout)); err != nil {
			printCLIError("%v", err)
			return 1
		}
		fmt.Printf("Updated %s\n", opts.source)
		return 0
	}

	// --extensions updates every package without touching pig itself.
	if opts.extensionsOnly && !opts.allPackages {
		if err := updatePackages(cwd, sm, "", packageProgressPrinter(os.Stdout)); err != nil {
			printCLIError("%v", err)
			return 1
		}
		fmt.Println("Updated packages")
		return 0
	}

	if !opts.allPackages {
		return runSelfUpdate(opts.force)
	}
	if err := updatePackages(cwd, sm, "", packageProgressPrinter(os.Stdout)); err != nil {
		printCLIError("%v", err)
		return 1
	}
	fmt.Println("Updated packages")
	return runSelfUpdate(opts.force)
}

func installAndPersistPackage(cwd string, sm *codingagent.SettingsManager, source string, local bool, progress packagemanager.ProgressCallback) error {
	pkg := codingagent.PackageSource{Source: source}
	if err := packagemanager.InstallPackageArtifacts(cwd, sm.AgentDir(), sm, pkg, local, progress); err != nil {
		return err
	}
	if err := verifyPackageContributesResources(cwd, sm, source, local); err != nil {
		return err
	}
	if _, err := addSourceToSettings(cwd, sm, source, local); err != nil {
		return err
	}
	return nil
}

// pig divergence (D57): reject a proven extension root that Package discovery
// cannot load.
func verifyPackageContributesResources(cwd string, sm *codingagent.SettingsManager, source string, local bool) error {
	root := installedSourceRoot(cwd, sm, source, local)
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

// installedSourceRoot returns the directory an install of source contributes, or "" when none exists. A local source is still the path as typed, relative to cwd; only the settings record rebases it onto the settings directory.
func installedSourceRoot(cwd string, sm *codingagent.SettingsManager, source string, local bool) string {
	if packagemanager.DetectSourceKind(source) != "local" {
		return packagemanager.InstalledPathForSource(cwd, sm.AgentDir(), sm, source, local)
	}
	root, err := packagemanager.ResolveLocalPackageRoot(cwd, source)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(root); err != nil {
		return ""
	}
	return root
}

// directoryIsProvablyAnExtension accepts a statically proven factory or standalone contract. Node factory resolution only selects an entrypoint; proving its exports requires execution, which Package installation must not perform.
func directoryIsProvablyAnExtension(root string) bool {
	definition, err := extsource.Resolve(root)
	return err == nil && (definition.Language != "node" || definition.Form != extsource.Factory)
}

func packageResourceCount(r packagecontent.Resources) int {
	return len(r.ExtensionEntries) + len(r.SkillDirs) + len(r.PromptFiles) + len(r.ThemeFiles) +
		len(r.AgentFiles) + len(r.MCPFiles) + len(r.HookFiles) + len(r.AgentEnvironments)
}

// removeAndPersistPackage runs the removal with the caller's settings before persisting, so failures retain both the package record and command overrides.
func removeAndPersistPackage(cwd string, sm *codingagent.SettingsManager, source string, local bool) (bool, error) {
	if err := removePackageArtifacts(cwd, sm, source, local); err != nil {
		return false, err
	}
	return removeSourceFromSettings(cwd, sm, source, local)
}

func updatePackages(cwd string, sm *codingagent.SettingsManager, source string, progress packagemanager.ProgressCallback) error {
	// update reads configured sources, not installation paths; pinned entries must not run legacy-root lookups.
	pkgs := configuredPackageSources(sm)
	if source == "" {
		return updateConfiguredSources(cwd, sm, pkgs, progress)
	}
	// package-manager.ts:1062-1077 resolves the input identity before scanning settings; resolvePath throws for an invalid file: URL.
	identity, err := packagemanager.PackageSourceIdentityChecked(cwd, source)
	if err != nil {
		return err
	}
	var matched []packagemanager.ConfiguredPackage
	for _, pkg := range pkgs {
		stored, err := packagemanager.PackageSourceIdentityChecked(packagemanager.SettingsBaseDir(cwd, sm.AgentDir(), pkg.Scope == "project"), pkg.Source.Source)
		if err != nil {
			return err
		}
		if stored == identity {
			matched = append(matched, pkg)
		}
	}
	if len(matched) == 0 {
		return errors.New(noMatchingPackageMessage(cwd, source, pkgs))
	}
	return updateConfiguredSources(cwd, sm, matched, progress)
}

type gitPackageSource struct {
	host   string
	path   string
	ref    string
	pinned bool
}

// normalizePackageSourceForSettings stores local package paths relative to the
// settings directory, matching upstream.
func normalizePackageSourceForSettings(baseDir, cwd, source string) string {
	kind := packagemanager.DetectSourceKind(source)
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

// addSourceToSettings reports whether it adds a source or replaces its ref, retaining filters and avoiding writes for identical sources.
func addSourceToSettings(cwd string, sm *codingagent.SettingsManager, source string, local bool) (bool, error) {
	baseDir := packagemanager.SettingsBaseDir(sm.CWD(), sm.AgentDir(), local)
	normalized := normalizePackageSourceForSettings(baseDir, cwd, source)
	current := sm.GetGlobalSettings().Packages
	if local {
		current = sm.GetProjectSettings().Packages
	}
	current = slices.Clone(current)
	// package-manager.ts:830 findIndex stops at the first match; PackageSourcesMatch throws for an invalid file: URL before it.
	index := -1
	for i, existing := range current {
		matched, err := packagemanager.PackageSourcesMatch(cwd, baseDir, existing.Source, source)
		if err != nil {
			return false, err
		}
		if matched {
			index = i
			break
		}
	}
	if index >= 0 {
		if current[index].Source == normalized {
			return false, nil
		}
		current[index].Source = normalized
	} else {
		current = append(current, codingagent.PackageSource{Source: normalized})
	}
	if local {
		return true, sm.SetProjectPackages(current)
	}
	return true, sm.SetPackages(current)
}

// removeSourceFromSettings ports package-manager.ts:855-871. PackageSourcesMatch (:1428-1433) resolves the stored key and then the input key for each entry, so an invalid file: URL fails the removal with fileURLToPath's error once any package is configured.
func removeSourceFromSettings(cwd string, sm *codingagent.SettingsManager, source string, local bool) (bool, error) {
	current := sm.GetGlobalSettings().Packages
	if local {
		current = sm.GetProjectSettings().Packages
	}
	baseDir := packagemanager.SettingsBaseDir(sm.CWD(), sm.AgentDir(), local)
	next := make([]codingagent.PackageSource, 0, len(current))
	for _, pkg := range current {
		matched, err := packagemanager.PackageSourcesMatch(cwd, baseDir, pkg.Source, source)
		if err != nil {
			return false, err
		}
		if !matched {
			next = append(next, pkg)
		}
	}
	if len(next) == len(current) {
		return false, nil
	}
	if local {
		return true, sm.SetProjectPackages(next)
	}
	return true, sm.SetPackages(next)
}

func listPackages(cwd string, sm *codingagent.SettingsManager) int {
	// package-manager.ts:977-1000 getInstalledPath resolves every local source, user then project, before anything prints.
	for _, pkg := range configuredPackageSources(sm) {
		if _, err := packagemanager.PackageSourceIdentityChecked(packagemanager.SettingsBaseDir(cwd, sm.AgentDir(), pkg.Scope == "project"), pkg.Source.Source); err != nil {
			printCLIError("%v", err)
			return 1
		}
	}
	pkgs := listConfiguredPackages(cwd, sm)
	if len(pkgs) == 0 {
		fmt.Println("No packages installed.")
		return 0
	}
	userPkgs := make([]packagemanager.ConfiguredPackage, 0)
	projectPkgs := make([]packagemanager.ConfiguredPackage, 0)
	for _, pkg := range pkgs {
		if pkg.Scope == "project" {
			projectPkgs = append(projectPkgs, pkg)
		} else {
			userPkgs = append(userPkgs, pkg)
		}
	}
	format := func(title string, pkgs []packagemanager.ConfiguredPackage) {
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

func listConfiguredPackages(cwd string, sm *codingagent.SettingsManager) []packagemanager.ConfiguredPackage {
	packages := configuredPackageSources(sm)
	for i := range packages {
		pkg := &packages[i]
		pkg.InstalledPath = packagemanager.InstalledPathForConfiguredSource(cwd, sm.AgentDir(), sm, pkg.Source.Source, pkg.Scope == "project")
	}
	return packages
}

func configuredPackageSources(sm *codingagent.SettingsManager) []packagemanager.ConfiguredPackage {
	global := sm.GetGlobalSettings().Packages
	project := sm.GetProjectSettings().Packages
	packages := make([]packagemanager.ConfiguredPackage, 0, len(global)+len(project))
	for _, pkg := range global {
		packages = append(packages, packagemanager.ConfiguredPackage{Source: pkg, Scope: "user"})
	}
	for _, pkg := range project {
		packages = append(packages, packagemanager.ConfiguredPackage{Source: pkg, Scope: "project"})
	}
	return packages
}

func removePackageArtifacts(cwd string, sm *codingagent.SettingsManager, source string, local bool) error {
	if local && !sm.IsProjectTrusted() {
		return errors.New("Project is not trusted; refusing to access project package storage")
	}
	switch packagemanager.DetectSourceKind(source) {
	case "npm":
		return uninstallManagedNPM(cwd, sm, source, local)
	case "git":
		checkout, err := packagemanager.GitCheckoutPath(cwd, sm.AgentDir(), source, local)
		if err != nil {
			return err
		}
		if configuredGitCheckoutInUse(cwd, sm, source, local) {
			return nil
		}
		if err := os.RemoveAll(checkout); err != nil {
			return err
		}
		if err := packagemanager.RemoveGitUpdateMarker(packagemanager.GitUpdateMarkerPath(checkout)); err != nil {
			return err
		}
		return packagemanager.PruneEmptyGitParents(checkout, codingagent.GitInstallRoot(cwd, sm.AgentDir(), local))
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
		if pkg.Scope != wantScope || packageMatchKeyForStoredBase(packagemanager.SettingsBaseDir(sm.CWD(), sm.AgentDir(), local), pkg.Source.Source) == packageMatchKeyForInput(cwd, removedSource) {
			continue
		}
		ref, err := sourceref.Parse(pkg.Source.Source, sourceref.Options{Bare: sourceref.BareReject})
		if err == nil && ref.Kind == sourceref.KindGit && ref.GitHost == removed.GitHost && ref.GitPath == removed.GitPath {
			return true
		}
	}
	return false
}

func resolveInputLocalPackageRoot(cwd, source string) string {
	root, _ := packagemanager.ResolveLocalPackageRoot(cwd, source)
	return root
}

func packageMatchKeyForStoredBase(baseDir, source string) string {
	return packagemanager.PackageSourceIdentity(baseDir, source)
}

func packageMatchKeyForInput(cwd, source string) string {
	return packageMatchKey(cwd, source, func() string { return cwd })
}

func packageMatchKey(cwd, source string, localBaseDir func() string) string {
	baseDir := localBaseDir()
	if baseDir == "" {
		baseDir = cwd
	}
	return packagemanager.PackageSourceIdentity(baseDir, source)
}

func noMatchingPackageMessage(cwd, source string, pkgs []packagemanager.ConfiguredPackage) string {
	if suggestion := findSuggestedConfiguredSource(source, pkgs); suggestion != "" {
		return fmt.Sprintf("No matching package found for %s. Did you mean %s?", source, suggestion)
	}
	return fmt.Sprintf("No matching package found for %s", source)
}

func findSuggestedConfiguredSource(source string, pkgs []packagemanager.ConfiguredPackage) string {
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

func parseGitPackageSource(source string) (gitPackageSource, bool) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return gitPackageSource{}, false
	}
	return gitPackageSource{
		host: ref.GitHost, path: ref.GitPath, ref: ref.GitRef, pinned: ref.GitRef != "",
	}, true
}

func uninstallManagedNPM(cwd string, sm *codingagent.SettingsManager, source string, local bool) error {
	ref, err := packagemanager.ParseNpmInstallRef(source)
	if err != nil {
		return err
	}
	installRoot := packagemanager.NpmInstallRoot(cwd, sm.AgentDir(), ref, local)
	if _, err := os.Stat(installRoot); os.IsNotExist(err) {
		return nil
	}
	command := packagemanager.DefaultNpmCommand(sm)
	args := append([]string{}, command[1:]...)
	manager, err := packagemanager.PackageManagerName(command)
	if err != nil {
		return err
	}
	if manager == "bun" {
		args = append(args, "uninstall", ref.NPMName, "--cwd", installRoot)
	} else {
		args = append(args, "uninstall", ref.NPMName, "--prefix", installRoot)
		if manager != "pnpm" {
			args = append(args, "--legacy-peer-deps")
		}
	}
	if ref.NPMRegistry != "" {
		args = append(args, "--registry", ref.NPMRegistry)
	}
	return packagemanager.RunPackageProcess("", command[0], args...)
}
