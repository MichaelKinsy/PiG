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

	"github.com/MichaelKinsy/PiG/coding/extension"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/tui"
)

func runConfigCommand(args []string) int {
	local := false
	var trustOverride *bool
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Println("Usage:\n  pig config [-l] [--approve|--no-approve]\n\nOpen the resource configuration TUI. Press Tab to switch global and project-local scope.")
			return 0
		case "-l", "--local":
			local = true
		case "-a", "--approve":
			trusted := true
			trustOverride = &trusted
		case "-na", "--no-approve":
			trusted := false
			trustOverride = &trusted
		default:
			fmt.Fprintf(os.Stderr, "pig config: unknown option %s\n", arg)
			return 1
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pig config:", err)
		return 1
	}
	agentDir := codingagent.AgentDir()
	globalSettings := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	projectTrusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: cwd, Store: codingagent.NewProjectTrustStore(agentDir), Override: trustOverride,
		Default: globalSettings.GetDefaultProjectTrust(), UI: extension.NoopUIContext,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "pig config:", err)
		return 1
	}
	if local && !projectTrusted {
		fmt.Fprintln(os.Stderr, "pig config: project is not trusted; use --approve to modify local resource config")
		return 1
	}
	settings := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, projectTrusted)
	reportSettingsErrors(settings, "config command")
	selector, err := newScopedConfigSelector(cwd, agentDir, globalSettings, settings, local, projectTrusted, cliBuiltinExtensionNames())
	if err != nil {
		fmt.Fprintln(os.Stderr, "pig config:", err)
		return 1
	}
	if err := runConfigSelectorTUI(selector, settings, agentDir); err != nil {
		fmt.Fprintln(os.Stderr, "pig config:", err)
		return 1
	}
	return 0
}

func newConfigSelector(cwd, agentDir string, sm *codingagent.SettingsManager) (*tui.ConfigSelectorComponent, error) {
	global := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	return newScopedConfigSelector(cwd, agentDir, global, sm, false, sm.IsProjectTrusted(), nil)
}

// newScopedConfigSelector builds the `pig config` selector. builtins are the names of the built-in extensions, which each scope lists
// as `builtin:<name>` extension resources after its other resources (package-manager-cli.ts:842-853, the builtinExtensions option
// of DefaultPackageManager).
func newScopedConfigSelector(cwd, agentDir string, global, settings *codingagent.SettingsManager, local, projectModeAvailable bool, builtins []string) (*tui.ConfigSelectorComponent, error) {
	globalItems, err := collectConfigResourceItems(cwd, agentDir, global)
	if err != nil {
		return nil, err
	}
	globalItems = append(globalItems, packagemanager.ResolveBuiltinExtensions(global, builtins)...)
	projectItems := globalItems
	if projectModeAvailable {
		projectItems, err = collectConfigResourceItems(cwd, agentDir, settings)
		if err != nil {
			return nil, err
		}
		projectItems = append(projectItems, packagemanager.ResolveBuiltinExtensions(settings, builtins)...)
		globalByKey := make(map[string]bool, len(globalItems))
		for _, item := range globalItems {
			globalByKey[configItemKey(item)] = item.Enabled
		}
		for i := range projectItems {
			inherited, found := globalByKey[configItemKey(projectItems[i])]
			projectItems[i].Inherited = found || projectItems[i].Scope == "user"
			if !found && projectItems[i].Scope == "project" {
				inherited = true
			}
			projectItems[i].InheritedEnabled = inherited
			projectItems[i].Override = projectConfigOverride(settings, &projectItems[i])
		}
	}
	writeScope := "global"
	if local {
		writeScope = "project"
	}
	selector := tui.NewScopedConfigSelector(tui.BuildResourceGroups(globalItems), tui.BuildResourceGroups(projectItems), 0, writeScope, projectModeAvailable)
	selector.OnToggle = func(item *tui.ResourceItem, enabled bool) {
		if err := applyConfigToggle(cwd, agentDir, settings, item, enabled); err != nil {
			item.Enabled = !enabled
			fmt.Fprintf(os.Stderr, "config toggle: %v\n", err)
		}
	}
	selector.OnOverride = func(item *tui.ResourceItem, state string) error {
		if err := applyProjectConfigOverride(cwd, settings, item, state); err != nil {
			fmt.Fprintf(os.Stderr, "config toggle: %v\n", err)
			return err
		}
		return nil
	}
	return selector, nil
}

func runConfigSelectorTUI(selector *tui.ConfigSelectorComponent, settings *codingagent.SettingsManager, agentDir string) error {
	ui := newConfigSelectorUI(selector, settings, agentDir)
	selector.SetTerminalRows(ui.Height())
	restore, err := tui.EnterRawMode()
	if err != nil {
		return err
	}
	defer restore()
	ui.HideCursor()
	return driveConfigSelector(ui, selector, os.Stdin)
}

// newConfigSelectorUI themes the process and builds the config selector's terminal UI, as Pi's selectConfig (config-selector.ts:20-30) does before it starts.
func newConfigSelectorUI(selector *tui.ConfigSelectorComponent, settings *codingagent.SettingsManager, agentDir string) *tui.TUI {
	initTheme(settings, agentDir)
	ui := tui.New()
	ui.SetLogDirectory(agentDir)
	ui.Add(selector)
	return ui
}

// normalizeConfigInputSequence applies upstream ProcessTerminal's native
// Shift+Enter normalization to one split input sequence.
var normalizeConfigInputSequence = tui.NormalizeProcessInputSequence

// driveConfigSelector runs the config selector's input loop against source.
//
// EnterRawMode pushes the Kitty flags that make a terminal report a release for
// every key, so raw reads must be split and filtered before they reach the
// component; handing a release to the selector moves its cursor a second time.
// Takes a reader so the loop a user drives is the loop under test.
func driveConfigSelector(ui *tui.TUI, selector *tui.ConfigSelectorComponent, source io.Reader) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer ui.Stop()
	done := false
	selector.OnCancel = func() { done = true }
	selector.OnExit = func() { done = true }
	var input *tui.TerminalInput
	input = tui.NewTerminalInput(func(chunk string) {
		if done {
			return
		}
		chunk = normalizeConfigInputSequence(chunk)
		if tui.ShouldDeliverKey(selector, chunk) {
			selector.HandleInput(chunk)
		}
		if done {
			input.Close()
		}
	})
	defer input.Close()
	readCh := make(chan []byte)
	errCh := make(chan error, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		errCh <- tui.ReadInputStream(ctx, source, func(data []byte) {
			select {
			case readCh <- data:
			case <-ctx.Done():
			}
		})
	}()
	defer func() { cancel(); <-readDone }()

	ui.Render()
	for !done {
		// A ready continuation precedes an expired framing deadline.
		select {
		case data := <-readCh:
			input.Process(data)
			ui.Render()
			continue
		default:
		}
		select {
		case data := <-readCh:
			input.Process(data)
			ui.Render()
		case err := <-errCh:
			input.FlushPending()
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case <-input.C:
			input.Flush()
			ui.Render()
		}
	}
	return nil
}

func collectConfigResourceItems(cwd, agentDir string, sm *codingagent.SettingsManager, resolvers ...extsource.ResolveFunc) ([]tui.ResourceItem, error) {
	// package-manager-cli.ts:842-850 runs packageManager.resolve(), which throws for an invalid settings file: URL.
	if err := validateConfiguredResourceEntries(cwd, agentDir, sm, sm.IsProjectTrusted()); err != nil {
		return nil, err
	}
	global := sm.GetGlobalSettings()
	project := sm.GetProjectSettings()
	items := make([]tui.ResourceItem, 0)
	seen := make(map[string]struct{})
	addItem := func(item tui.ResourceItem) {
		key := string(item.ResourceType) + ":" + item.Path
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}

	projectBase, projectResourcesEnabled := codingagent.ProjectConfigDir(cwd), sm.IsProjectTrusted()
	userBase := agentDir

	appendTopLevel := func(entries []string, scope, source, baseDir, kind string, autoPaths []string) {
		resourceType := tui.ResourceType(kind)
		plain, patterns := splitResourcePatterns(entries)
		if source != "local" {
			plain = nil
		}
		resolved := make([]string, 0, len(plain))
		for _, entry := range plain {
			resolved = append(resolved, resolveSettingsPath(baseDir, entry))
		}
		enabledPaths := applyResourcePatterns(collectResourceFilesFromPaths(resolved, kind), patterns, baseDir, kind)
		for _, path := range collectResourceFilesFromPaths(resolved, kind) {
			addItem(tui.ResourceItem{
				Path:         path,
				Enabled:      slices.Contains(enabledPaths, path),
				ResourceType: resourceType,
				Scope:        scope,
				Origin:       "top-level",
				Source:       source,
				BaseDir:      baseDir,
			})
		}
		for _, path := range autoPaths {
			addItem(tui.ResourceItem{
				Path:         path,
				Enabled:      isEnabledByOverrides(path, entries, baseDir, kind),
				ResourceType: resourceType,
				Scope:        scope,
				Origin:       "top-level",
				Source:       source,
				BaseDir:      baseDir,
			})
		}
	}

	if projectResourcesEnabled {
		appendTopLevel(project.Extensions, "project", "local", projectBase, "extensions", nil)
		appendTopLevel(project.Skills, "project", "local", projectBase, "skills", nil)
		appendTopLevel(project.Prompts, "project", "local", projectBase, "prompts", nil)
		appendTopLevel(project.Themes, "project", "local", projectBase, "themes", nil)
	}
	appendTopLevel(global.Extensions, "user", "local", userBase, "extensions", nil)
	appendTopLevel(global.Skills, "user", "local", userBase, "skills", nil)
	appendTopLevel(global.Prompts, "user", "local", userBase, "prompts", nil)
	appendTopLevel(global.Themes, "user", "local", userBase, "themes", nil)

	userAgentsSkills := filepath.Join(packageManagerHomeDir(), ".agents", "skills")
	projectAgentSkillDirs := make([]string, 0)
	for _, dir := range discoverAncestorAgentsSkillDirs(cwd) {
		if samePath(dir, userAgentsSkills) {
			continue
		}
		projectAgentSkillDirs = append(projectAgentSkillDirs, dir)
	}
	if projectResourcesEnabled {
		appendTopLevel(project.Extensions, "project", "auto", projectBase, "extensions", collectAutoDiscoveredResourcePaths(filepath.Join(projectBase, "extensions"), "extensions"))
		appendTopLevel(project.Skills, "project", "auto", projectBase, "skills", collectAutoDiscoveredResourcePaths(filepath.Join(projectBase, "skills"), "skills"))
		for _, dir := range projectAgentSkillDirs {
			appendTopLevel(project.Skills, "project", "auto", filepath.Dir(dir), "skills", discoverSkillDir(dir))
		}
		appendTopLevel(project.Prompts, "project", "auto", projectBase, "prompts", collectAutoDiscoveredResourcePaths(filepath.Join(projectBase, "prompts"), "prompts"))
		appendTopLevel(project.Themes, "project", "auto", projectBase, "themes", collectAutoDiscoveredResourcePaths(filepath.Join(projectBase, "themes"), "themes"))
	}
	appendTopLevel(global.Extensions, "user", "auto", userBase, "extensions", collectAutoDiscoveredResourcePaths(filepath.Join(userBase, "extensions"), "extensions"))
	appendTopLevel(global.Skills, "user", "auto", userBase, "skills", collectAutoDiscoveredResourcePaths(filepath.Join(userBase, "skills"), "skills"))
	appendTopLevel(global.Skills, "user", "auto", filepath.Dir(userAgentsSkills), "skills", discoverSkillDir(userAgentsSkills))
	appendTopLevel(global.Prompts, "user", "auto", userBase, "prompts", collectAutoDiscoveredResourcePaths(filepath.Join(userBase, "prompts"), "prompts"))
	appendTopLevel(global.Themes, "user", "auto", userBase, "themes", collectAutoDiscoveredResourcePaths(filepath.Join(userBase, "themes"), "themes"))

	pkgItems, err := packagemanager.CollectResolvedPackageResourceItems(cwd, sm.AgentDir(), sm, nil, true, resolvers...)
	if err != nil {
		return nil, err
	}
	for _, item := range pkgItems {
		addItem(item)
	}
	return canonicalResourceItems(items), nil
}

func samePath(a, b string) bool {
	aa, err1 := nodepath.Resolve(a)
	bb, err2 := nodepath.Resolve(b)
	return err1 == nil && err2 == nil && aa == bb
}

func applyConfigToggle(cwd, agentDir string, sm *codingagent.SettingsManager, item *tui.ResourceItem, enabled bool) error {
	if item.Origin == "package" {
		return applyPackageToggle(cwd, sm, item, enabled)
	}
	return applyTopLevelToggle(cwd, agentDir, sm, item, enabled)
}

func applyTopLevelToggle(cwd, agentDir string, sm *codingagent.SettingsManager, item *tui.ResourceItem, enabled bool) error {
	baseDir := agentDir
	if item.Scope == "project" {
		baseDir = codingagent.ProjectConfigDir(cwd)
	}
	pattern, err := filepath.Rel(baseDir, item.Path)
	if err != nil || item.Source == "builtin" {
		// A built-in extension path is its own pattern (config-selector.ts:863-866).
		pattern = item.Path
	}
	pattern = filepath.ToSlash(pattern)
	var current []string
	var setter func([]string) error
	switch item.ResourceType {
	case tui.ResourceExtensions:
		if item.Scope == "project" {
			current = append([]string{}, sm.GetProjectSettings().Extensions...)
			setter = sm.SetProjectExtensionPaths
		} else {
			current = append([]string{}, sm.GetGlobalSettings().Extensions...)
			setter = sm.SetExtensionPaths
		}
	case tui.ResourceSkills:
		if item.Scope == "project" {
			current = append([]string{}, sm.GetProjectSettings().Skills...)
			setter = sm.SetProjectSkillPaths
		} else {
			current = append([]string{}, sm.GetGlobalSettings().Skills...)
			setter = sm.SetSkillPaths
		}
	case tui.ResourcePrompts:
		if item.Scope == "project" {
			current = append([]string{}, sm.GetProjectSettings().Prompts...)
			setter = sm.SetProjectPromptTemplatePaths
		} else {
			current = append([]string{}, sm.GetGlobalSettings().Prompts...)
			setter = sm.SetPromptTemplatePaths
		}
	case tui.ResourceThemes:
		if item.Scope == "project" {
			current = append([]string{}, sm.GetProjectSettings().Themes...)
			setter = sm.SetProjectThemePaths
		} else {
			current = append([]string{}, sm.GetGlobalSettings().Themes...)
			setter = sm.SetThemePaths
		}
	default:
		return nil
	}
	// Pi drops the entries that name the resource, with or without an override prefix, then records the new state as `+pattern` or
	// `-pattern` (config-selector.ts:545-560).
	current = slices.DeleteFunc(current, func(entry string) bool {
		target := patternEntryTarget(entry)
		return filepath.ToSlash(target) == pattern || filepath.ToSlash(resolveSettingsPath(baseDir, target)) == filepath.ToSlash(item.Path)
	})
	if enabled {
		current = append(current, "+"+pattern)
	} else {
		current = append(current, "-"+pattern)
	}
	return setter(current)
}

func applyPackageToggle(cwd string, sm *codingagent.SettingsManager, item *tui.ResourceItem, enabled bool) error {
	pkgs := listConfiguredPackages(cwd, sm)
	var target *packagemanager.ConfiguredPackage
	for i := range pkgs {
		if pkgs[i].Scope == item.Scope && pkgs[i].Source.Source == item.Source {
			target = &pkgs[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("package %s not found", item.Source)
	}
	pattern := item.Pattern
	if pattern == "" {
		relTarget := item.Path
		if item.ResourceType == tui.ResourceSkills {
			relTarget = packagecontent.SkillFile(item.Path)
		}
		var err error
		pattern, err = filepath.Rel(target.InstalledPath, relTarget)
		if err != nil {
			pattern = relTarget
		}
		pattern = filepath.ToSlash(pattern)
	}
	pkg := target.Source
	switch item.ResourceType {
	case tui.ResourceExtensions:
		pkg.Extensions = updateResourcePatterns(pkg.Extensions, pattern, enabled)
	case tui.ResourceSkills:
		pkg.Skills = updateResourcePatterns(pkg.Skills, pattern, enabled)
	case tui.ResourcePrompts:
		pkg.Prompts = updateResourcePatterns(pkg.Prompts, pattern, enabled)
	case tui.ResourceThemes:
		pkg.Themes = updateResourcePatterns(pkg.Themes, pattern, enabled)
	}
	if target.Scope == "project" {
		pkgs2 := sm.GetProjectSettings().Packages
		for i := range pkgs2 {
			if pkgs2[i].Source == item.Source {
				pkgs2[i] = pkg
			}
		}
		if err := sm.SetProjectPackages(pkgs2); err != nil {
			return err
		}
	} else {
		pkgs2 := sm.GetGlobalSettings().Packages
		for i := range pkgs2 {
			if pkgs2[i].Source == item.Source {
				pkgs2[i] = pkg
			}
		}
		if err := sm.SetPackages(pkgs2); err != nil {
			return err
		}
	}
	// No materialization step: package resources are resolved on demand from the
	// installed package root on the next reload/startup pass.
	_ = item
	return nil
}

// patternEntryTarget is the entry without its one `!`, `+` or `-` override prefix (config-selector.ts:846-848, getPatternEntryTarget).
func patternEntryTarget(entry string) string {
	if strings.HasPrefix(entry, "!") || strings.HasPrefix(entry, "+") || strings.HasPrefix(entry, "-") {
		return entry[1:]
	}
	return entry
}

func configItemKey(item tui.ResourceItem) string {
	return string(item.ResourceType) + "\x00" + canonicalStatusPath(item.Path)
}

func projectConfigOverride(sm *codingagent.SettingsManager, item *tui.ResourceItem) string {
	var entries []string
	if item.Origin == "package" {
		sourceBase := packagemanager.SettingsBaseDir(sm.CWD(), sm.AgentDir(), item.Scope == "project")
		for _, pkg := range sm.GetProjectSettings().Packages {
			if packagemanager.PackageSourceIdentity(packagemanager.SettingsBaseDir(sm.CWD(), sm.AgentDir(), true), pkg.Source) != packagemanager.PackageSourceIdentity(sourceBase, item.Source) && pkg.Source != item.Source {
				continue
			}
			switch item.ResourceType {
			case tui.ResourceExtensions:
				entries = pkg.Extensions
			case tui.ResourceSkills:
				entries = pkg.Skills
			case tui.ResourcePrompts:
				entries = pkg.Prompts
			case tui.ResourceThemes:
				entries = pkg.Themes
			}
			break
		}
	} else {
		switch item.ResourceType {
		case tui.ResourceExtensions:
			entries = sm.GetProjectSettings().Extensions
		case tui.ResourceSkills:
			entries = sm.GetProjectSettings().Skills
		case tui.ResourcePrompts:
			entries = sm.GetProjectSettings().Prompts
		case tui.ResourceThemes:
			entries = sm.GetProjectSettings().Themes
		}
	}
	pattern := item.Pattern
	if pattern == "" {
		base := item.BaseDir
		if item.Origin != "package" {
			base = codingagent.ProjectConfigDir(sm.CWD())
		}
		pattern, _ = filepath.Rel(base, item.Path)
		if item.ResourceType == tui.ResourceSkills && item.Origin == "package" {
			pattern, _ = filepath.Rel(base, packagecontent.SkillFile(item.Path))
		}
		pattern = filepath.ToSlash(pattern)
		if item.Source == "builtin" {
			pattern = item.Path
		}
	}
	state := "inherit"
	for _, entry := range entries {
		target := strings.TrimLeft(entry, "+-!")
		if filepath.ToSlash(target) != pattern && canonicalStatusPath(resolveSettingsPath(codingagent.ProjectConfigDir(sm.CWD()), target)) != canonicalStatusPath(item.Path) {
			continue
		}
		if strings.HasPrefix(entry, "-") || strings.HasPrefix(entry, "!") {
			state = "unload"
		} else {
			state = "load"
		}
	}
	return state
}

func applyProjectConfigOverride(cwd string, sm *codingagent.SettingsManager, item *tui.ResourceItem, state string) error {
	if item.Origin == "package" {
		packages := sm.GetProjectSettings().Packages
		sourceBase := packagemanager.SettingsBaseDir(sm.CWD(), sm.AgentDir(), item.Scope == "project")
		index := slices.IndexFunc(packages, func(pkg codingagent.PackageSource) bool {
			return pkg.Source == item.Source || packagemanager.PackageSourceIdentity(packagemanager.SettingsBaseDir(sm.CWD(), sm.AgentDir(), true), pkg.Source) == packagemanager.PackageSourceIdentity(sourceBase, item.Source)
		})
		if index < 0 {
			if state == "inherit" {
				return nil
			}
			overrideSource := item.Source
			if packagemanager.DetectSourceKind(item.Source) == "local" {
				resolved, err := packagemanager.ResolveLocalPackageRoot(sourceBase, item.Source)
				if err != nil {
					return err
				}
				if relative, err := filepath.Rel(packagemanager.SettingsBaseDir(sm.CWD(), sm.AgentDir(), true), resolved); err == nil {
					overrideSource = relative
				}
			}
			packages = append(packages, codingagent.PackageSource{Source: overrideSource, Autoload: new(false)})
			index = len(packages) - 1
		}
		pkg := packages[index]
		pattern := item.Pattern
		if pattern == "" {
			target := item.Path
			if item.ResourceType == tui.ResourceSkills {
				target = packagecontent.SkillFile(target)
			}
			pattern, _ = filepath.Rel(item.BaseDir, target)
		}
		if _, err := packagecontent.ApplyConfiguredDelta(packagecontent.Kind(item.ResourceType), []string{filepath.ToSlash(pattern)}, nil, []string{"+" + filepath.ToSlash(pattern)}); err != nil {
			return err
		}
		update := func(entries []string) []string {
			out := slices.DeleteFunc(slices.Clone(entries), func(entry string) bool {
				return filepath.ToSlash(strings.TrimLeft(entry, "+-!")) == filepath.ToSlash(pattern)
			})
			if state != "inherit" {
				out = append(out, map[bool]string{true: "+", false: "-"}[state == "load"]+pattern)
			}
			if len(out) == 0 {
				return nil
			}
			return out
		}
		switch item.ResourceType {
		case tui.ResourceExtensions:
			pkg.Extensions = update(pkg.Extensions)
		case tui.ResourceSkills:
			pkg.Skills = update(pkg.Skills)
		case tui.ResourcePrompts:
			pkg.Prompts = update(pkg.Prompts)
		case tui.ResourceThemes:
			pkg.Themes = update(pkg.Themes)
		}
		if pkg.Extensions == nil && pkg.Skills == nil && pkg.Prompts == nil && pkg.Themes == nil {
			if pkg.Autoload != nil && !*pkg.Autoload {
				packages = slices.Delete(packages, index, index+1)
			} else {
				packages[index] = codingagent.PackageSource{Source: pkg.Source}
			}
		} else {
			packages[index] = pkg
		}
		return sm.SetProjectPackages(packages)
	}
	project := sm.GetProjectSettings()
	var entries []string
	var setter func([]string) error
	switch item.ResourceType {
	case tui.ResourceExtensions:
		entries, setter = project.Extensions, sm.SetProjectExtensionPaths
	case tui.ResourceSkills:
		entries, setter = project.Skills, sm.SetProjectSkillPaths
	case tui.ResourcePrompts:
		entries, setter = project.Prompts, sm.SetProjectPromptTemplatePaths
	case tui.ResourceThemes:
		entries, setter = project.Themes, sm.SetProjectThemePaths
	}
	// Overrides are persisted as Pi writes them: the path or base-relative path
	// with the platform separator. Pi matches override entries by exact string.
	projectBase := codingagent.ProjectConfigDir(cwd)
	pattern := item.Path
	if !item.Inherited && item.Scope == "project" && item.Source != "builtin" {
		if relative, err := filepath.Rel(projectBase, item.Path); err == nil {
			pattern = relative
		}
	}
	entries = slices.DeleteFunc(slices.Clone(entries), func(entry string) bool {
		target := strings.TrimLeft(entry, "+-!")
		if item.Source == "builtin" {
			return target == item.Path
		}
		return canonicalStatusPath(resolveSettingsPath(projectBase, target)) == canonicalStatusPath(item.Path)
	})
	if state != "inherit" {
		// Project entries name inherited files to override them. Built-in paths need no entry (config-selector.ts:688-690).
		if item.Inherited && item.Source != "builtin" && !slices.Contains(entries, pattern) {
			entries = append(entries, pattern)
		}
		entries = append(entries, map[bool]string{true: "+", false: "-"}[state == "load"]+pattern)
	}
	return setter(entries)
}

func updateResourcePatterns(patterns []string, pattern string, enabled bool) []string {
	updated := make([]string, 0, len(patterns)+1)
	for _, existing := range patterns {
		stripped := strings.TrimLeft(existing, "+-!")
		if filepath.ToSlash(stripped) == pattern {
			continue
		}
		updated = append(updated, existing)
	}
	prefix := "-"
	if enabled {
		prefix = "+"
	}
	updated = append(updated, prefix+pattern)
	return updated
}
