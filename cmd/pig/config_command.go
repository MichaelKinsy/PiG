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
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
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
	selector, err := newScopedConfigSelector(cwd, agentDir, globalSettings, settings, local, projectTrusted)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pig config:", err)
		return 1
	}
	if err := runConfigSelectorTUI(selector, settings.Get().Theme, agentDir); err != nil {
		fmt.Fprintln(os.Stderr, "pig config:", err)
		return 1
	}
	return 0
}

func newConfigSelector(cwd, agentDir string, sm *codingagent.SettingsManager) (*tui.ConfigSelectorComponent, error) {
	global := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	return newScopedConfigSelector(cwd, agentDir, global, sm, false, sm.IsProjectTrusted())
}

func newScopedConfigSelector(cwd, agentDir string, global, settings *codingagent.SettingsManager, local, projectModeAvailable bool) (*tui.ConfigSelectorComponent, error) {
	globalItems, err := collectConfigResourceItems(cwd, agentDir, global)
	if err != nil {
		return nil, err
	}
	projectItems := globalItems
	if projectModeAvailable {
		projectItems, err = collectConfigResourceItems(cwd, agentDir, settings)
		if err != nil {
			return nil, err
		}
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

func runConfigSelectorTUI(selector *tui.ConfigSelectorComponent, themeName, agentDir string) error {
	if themeName != "" {
		tui.SetThemeByName(themeName)
	} else {
		tui.DetectTheme()
	}
	if themesDir := filepath.Join(agentDir, "themes"); true {
		_ = tui.ActiveThemeRegistry().LoadDir(themesDir)
		if themeName != "" {
			tui.SetThemeByName(themeName)
		}
	}
	ui := tui.New()
	ui.SetLogDirectory(agentDir)
	ui.Add(selector)
	selector.SetTerminalRows(ui.Height())
	restore, err := tui.EnterRawMode()
	if err != nil {
		return err
	}
	defer restore()
	ui.HideCursor()
	defer ui.ShowCursor()
	return driveConfigSelector(ui, selector, os.Stdin)
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
	done := false
	selector.OnCancel = func() { done = true }
	selector.OnExit = func() { done = true }
	stdinBuf := codingagent.NewStdinBuffer(codingagent.StdinBufferOptions{
		EscapeTimeout: time.Duration(tui.ResolveEscapeTimeoutMs(os.Getenv) * float64(time.Millisecond)),
	})
	dispatch := func(chunks []string) {
		for _, chunk := range chunks {
			// Mirrors upstream ProcessTerminal.forwardInputSequence.
			chunk = normalizeConfigInputSequence(chunk)
			if !tui.ShouldDeliverKey(selector, chunk) {
				continue
			}
			selector.HandleInput(chunk)
			if done {
				return
			}
		}
	}
	type readResult struct {
		data []byte
		err  error
	}
	readCh := make(chan readResult, 1)
	readNext := func() {
		go func() {
			data, err := tui.ReadInput(source)
			readCh <- readResult{data: data, err: err}
		}()
	}
	var flushTimer *time.Timer
	var flushC <-chan time.Time
	stopFlush := func() {
		if flushTimer != nil {
			flushTimer.Stop()
		}
		flushTimer = nil
		flushC = nil
	}
	syncFlush := func() {
		stopFlush()
		if stdinBuf.HasPendingFlush() {
			flushTimer = time.NewTimer(stdinBuf.FlushTimeout())
			flushC = flushTimer.C
		}
	}
	defer stopFlush()

	ui.Render()
	readNext()
	for !done {
		select {
		case result := <-readCh:
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					dispatch(stdinBuf.Flush())
					return nil
				}
				return result.err
			}
			dispatch(stdinBuf.ProcessTerminalBytes(result.data))
			syncFlush()
			ui.Render()
			if !done {
				readNext()
			}
		case <-flushC:
			stopFlush()
			dispatch(stdinBuf.Flush())
			ui.Render()
		}
	}
	return nil
}

func collectConfigResourceItems(cwd, agentDir string, sm *codingagent.SettingsManager, resolvers ...extsource.ResolveFunc) ([]tui.ResourceItem, error) {
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

	projectBase, projectResourcesEnabled := projectResourceRoot(cwd)
	projectResourcesEnabled = projectResourcesEnabled && sm.IsProjectTrusted()
	userBase := agentDir

	appendTopLevel := func(entries []string, scope, source, baseDir, kind string, autoPaths []string) {
		resourceType := tui.ResourceType(kind)
		plain, patterns := splitResourcePatterns(entries)
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

	home, _ := os.UserHomeDir()
	userAgentsSkills := filepath.Join(home, ".agents", "skills")
	projectAgentSkillDirs := make([]string, 0)
	for _, dir := range discoverAncestorAgentsSkillDirs(cwd) {
		if samePath(dir, userAgentsSkills) {
			continue
		}
		projectAgentSkillDirs = append(projectAgentSkillDirs, discoverSkillDir(dir)...)
	}
	if projectResourcesEnabled {
		appendTopLevel(project.Extensions, "project", "auto", projectBase, "extensions", collectAutoDiscoveredResourcePaths(filepath.Join(projectBase, "extensions"), "extensions"))
		appendTopLevel(project.Skills, "project", "auto", projectBase, "skills", collectAutoDiscoveredResourcePaths(filepath.Join(projectBase, "skills"), "skills"))
		for _, path := range projectAgentSkillDirs {
			appendTopLevel(project.Skills, "project", "auto", filepath.Dir(filepath.Dir(path)), "skills", []string{path})
		}
		appendTopLevel(project.Prompts, "project", "auto", projectBase, "prompts", collectAutoDiscoveredResourcePaths(filepath.Join(projectBase, "prompts"), "prompts"))
		appendTopLevel(project.Themes, "project", "auto", projectBase, "themes", collectAutoDiscoveredResourcePaths(filepath.Join(projectBase, "themes"), "themes"))
	}
	appendTopLevel(global.Extensions, "user", "auto", userBase, "extensions", collectAutoDiscoveredResourcePaths(filepath.Join(userBase, "extensions"), "extensions"))
	appendTopLevel(global.Skills, "user", "auto", userBase, "skills", collectAutoDiscoveredResourcePaths(filepath.Join(userBase, "skills"), "skills"))
	for _, path := range discoverSkillDir(userAgentsSkills) {
		appendTopLevel(global.Skills, "user", "auto", filepath.Dir(filepath.Dir(path)), "skills", []string{path})
	}
	appendTopLevel(global.Prompts, "user", "auto", userBase, "prompts", collectAutoDiscoveredResourcePaths(filepath.Join(userBase, "prompts"), "prompts"))
	appendTopLevel(global.Themes, "user", "auto", userBase, "themes", collectAutoDiscoveredResourcePaths(filepath.Join(userBase, "themes"), "themes"))

	projectPkgs := make([]configuredPackage, 0)
	userPkgs := make([]configuredPackage, 0)
	for _, pkg := range listConfiguredPackages(cwd, sm) {
		if pkg.Scope == "project" {
			projectPkgs = append(projectPkgs, pkg)
		} else {
			userPkgs = append(userPkgs, pkg)
		}
	}
	for _, pkg := range append(projectPkgs, userPkgs...) {
		root := pkg.InstalledPath
		if root == "" {
			return nil, fmt.Errorf("%s Package %q is not materialized; run pig update %s", pkg.Scope, pkg.Source.Source, pkg.Source.Source)
		}
		pkgItems, err := collectPackageResourceItems(root, pkg, resolvers...)
		if err != nil {
			return nil, err
		}
		for _, item := range pkgItems {
			addItem(item)
		}
	}
	return items, nil
}

func samePath(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}

func collectPackageResourceItems(root string, pkg configuredPackage, resolvers ...extsource.ResolveFunc) ([]tui.ResourceItem, error) {
	filters, err := effectiveConfiguredPackageFilters(pkg, resolvers...)
	if err != nil {
		return nil, err
	}
	resources, missing, err := packagecontent.InspectConfiguredWithResolver(root, filters, configuredExtensionResolver(resolvers))
	if err != nil {
		return nil, err
	}
	out := make([]tui.ResourceItem, 0)
	for _, path := range resources.ExtensionEntries {
		rel, _ := filepath.Rel(root, path)
		out = append(out, tui.ResourceItem{Path: path, Enabled: packagecontent.ResourceEnabled(rel, filters[packagecontent.Extensions]), ResourceType: tui.ResourceExtensions, Scope: pkg.Scope, Origin: "package", Source: pkg.Source.Source, BaseDir: root})
	}
	for _, path := range resources.SkillDirs {
		rel, _ := filepath.Rel(root, filepath.Join(path, "SKILL.md"))
		out = append(out, tui.ResourceItem{Path: path, Enabled: packagecontent.ResourceEnabled(rel, filters[packagecontent.Skills]), ResourceType: tui.ResourceSkills, Scope: pkg.Scope, Origin: "package", Source: pkg.Source.Source, BaseDir: root})
	}
	for _, path := range resources.PromptFiles {
		rel, _ := filepath.Rel(root, path)
		out = append(out, tui.ResourceItem{Path: path, Enabled: packagecontent.ResourceEnabled(rel, filters[packagecontent.Prompts]), ResourceType: tui.ResourcePrompts, Scope: pkg.Scope, Origin: "package", Source: pkg.Source.Source, BaseDir: root})
	}
	for _, path := range resources.ThemeFiles {
		rel, _ := filepath.Rel(root, path)
		out = append(out, tui.ResourceItem{Path: path, Enabled: packagecontent.ResourceEnabled(rel, filters[packagecontent.Themes]), ResourceType: tui.ResourceThemes, Scope: pkg.Scope, Origin: "package", Source: pkg.Source.Source, BaseDir: root})
	}
	for _, member := range missing {
		resourceType := tui.ResourceType(member.Kind)
		memberPath := member.Path
		if member.Kind == packagecontent.Skills {
			memberPath = filepath.Dir(memberPath)
		}
		out = append(out, tui.ResourceItem{
			Path: memberPath, Pattern: member.Pattern, Enabled: member.Enabled,
			ResourceType: resourceType, Scope: pkg.Scope, Origin: "package",
			Source: pkg.Source.Source, BaseDir: root, Health: "missing",
		})
	}
	return out, nil
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
	if err != nil {
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
	current = slices.DeleteFunc(current, func(s string) bool {
		return filepath.ToSlash(s) == pattern || filepath.ToSlash(resolveSettingsPath(baseDir, s)) == filepath.ToSlash(item.Path)
	})
	if enabled {
		current = append(current, pattern)
	}
	return setter(current)
}

func applyPackageToggle(cwd string, sm *codingagent.SettingsManager, item *tui.ResourceItem, enabled bool) error {
	pkgs := listConfiguredPackages(cwd, sm)
	var target *configuredPackage
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
			relTarget = filepath.Join(item.Path, "SKILL.md")
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

func configItemKey(item tui.ResourceItem) string {
	return string(item.ResourceType) + "\x00" + canonicalStatusPath(item.Path)
}

func projectConfigOverride(sm *codingagent.SettingsManager, item *tui.ResourceItem) string {
	var entries []string
	if item.Origin == "package" {
		sourceBase := settingsBaseDirForManager(sm, item.Scope == "project")
		for _, pkg := range sm.GetProjectSettings().Packages {
			if packageSourceIdentity(settingsBaseDirForManager(sm, true), pkg.Source) != packageSourceIdentity(sourceBase, item.Source) && pkg.Source != item.Source {
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
			pattern = filepath.Join(pattern, "SKILL.md")
		}
		pattern = filepath.ToSlash(pattern)
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
		sourceBase := settingsBaseDirForManager(sm, item.Scope == "project")
		index := slices.IndexFunc(packages, func(pkg codingagent.PackageSource) bool {
			return pkg.Source == item.Source || packageSourceIdentity(settingsBaseDirForManager(sm, true), pkg.Source) == packageSourceIdentity(sourceBase, item.Source)
		})
		if index < 0 {
			if state == "inherit" {
				return nil
			}
			overrideSource := item.Source
			if detectSourceKind(item.Source) == "local" {
				resolved, err := resolveLocalPackageRoot(sourceBase, item.Source)
				if err != nil {
					return err
				}
				if relative, err := filepath.Rel(settingsBaseDirForManager(sm, true), resolved); err == nil {
					overrideSource = relative
				}
			}
			packages = append(packages, codingagent.PackageSource{Source: overrideSource})
			index = len(packages) - 1
		}
		pkg := packages[index]
		pattern := item.Pattern
		if pattern == "" {
			target := item.Path
			if item.ResourceType == tui.ResourceSkills {
				target = filepath.Join(target, "SKILL.md")
			}
			pattern, _ = filepath.Rel(item.BaseDir, target)
		}
		update := func(entries []string) []string {
			out := slices.DeleteFunc(slices.Clone(entries), func(entry string) bool {
				return filepath.ToSlash(strings.TrimLeft(entry, "+-!")) == filepath.ToSlash(pattern)
			})
			if state != "inherit" {
				out = append(out, map[bool]string{true: "+", false: "-"}[state == "load"]+pattern)
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
		if state == "inherit" && len(pkg.Extensions) == 0 && len(pkg.Skills) == 0 && len(pkg.Prompts) == 0 && len(pkg.Themes) == 0 {
			packages = append(packages[:index], packages[index+1:]...)
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
	if !item.Inherited && item.Scope == "project" {
		if relative, err := filepath.Rel(projectBase, item.Path); err == nil {
			pattern = relative
		}
	}
	entries = slices.DeleteFunc(slices.Clone(entries), func(entry string) bool {
		target := strings.TrimLeft(entry, "+-!")
		return canonicalStatusPath(resolveSettingsPath(projectBase, target)) == canonicalStatusPath(item.Path)
	})
	if state != "inherit" {
		if item.Inherited && !slices.Contains(entries, pattern) {
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
