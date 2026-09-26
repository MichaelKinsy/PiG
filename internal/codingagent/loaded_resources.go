package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts
// showLoadedResources and its path and label helpers.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/tui"
)

// expandableText is upstream's ExpandableText: a Text whose content is
// rebuilt from its collapsed or expanded producer on every expansion change.
type expandableText struct {
	*tui.Text
	collapsed func() string
	expanded  func() string
}

func newExpandableText(collapsed, expanded func() string, isExpanded bool) *expandableText {
	text := &expandableText{collapsed: collapsed, expanded: expanded}
	content := collapsed
	if isExpanded {
		content = expanded
	}
	text.Text = tui.NewPaddedText(content(), 0, 0, nil)
	return text
}

func (t *expandableText) SetExpanded(expanded bool) {
	if expanded {
		t.SetText(t.expanded())
		return
	}
	t.SetText(t.collapsed())
}

// loadedResource is one listed resource path. info is nil when upstream has
// no SourceInfo for the path.
type loadedResource struct {
	path string
	info *PiSourceInfo
}

type loadedScopeGroup struct {
	scope    string
	paths    []loadedResource
	packages map[string][]loadedResource
}

// startupExpansionState is upstream getStartupExpansionState.
func (m *InteractiveMode) startupExpansionState() bool {
	m.toolMu.Lock()
	defer m.toolMu.Unlock()
	return m.opts.Verbose || m.toolsExpanded
}

// showLoadedResources rebuilds the loaded-resources listing between the
// header and the transcript: one expandable section per resource kind, each
// followed by a blank line. Quiet startup omits it unless force is set.
func (m *InteractiveMode) showLoadedResources(force bool) {
	if m.loadedResourcesContainer == nil {
		return
	}
	m.loadedResourcesContainer.Clear()
	m.loadedResourceSections = nil
	if !force && !m.opts.Verbose && m.opts.Settings.QuietStartup {
		return
	}
	theme := tui.ActiveTheme()
	collator := collate.New(language.Und)
	sectionHeader := func(name string) string { return theme.FgText("mdHeading", "["+name+"]") }
	formatCompactList := func(items []string, sorted bool) string {
		labels := make([]string, 0, len(items))
		for _, item := range items {
			if label := strings.TrimSpace(item); label != "" {
				labels = append(labels, label)
			}
		}
		if sorted {
			slices.SortStableFunc(labels, collator.CompareString)
		}
		return theme.FgText("dim", "  "+strings.Join(labels, ", "))
	}
	expanded := m.startupExpansionState()
	addLoadedSection := func(name, collapsedBody, expandedBody string) {
		section := newExpandableText(
			func() string { return sectionHeader(name) + "\n" + collapsedBody },
			func() string { return sectionHeader(name) + "\n" + expandedBody },
			expanded,
		)
		m.loadedResourceSections = append(m.loadedResourceSections, section)
		m.loadedResourcesContainer.Add(section)
		m.loadedResourcesContainer.Add(tui.NewSpacer(1))
	}

	contextPaths := append(slices.Clone(m.opts.SystemPromptSourcePaths), contextFilePaths(m.opts.ContextFiles)...)
	if len(contextPaths) > 0 {
		m.loadedResourcesContainer.Add(tui.NewSpacer(1))
		expandedLines := make([]string, len(contextPaths))
		compact := make([]string, len(contextPaths))
		for i, path := range contextPaths {
			expandedLines[i] = theme.FgText("dim", "  "+formatDisplayPath(path))
			compact[i] = m.formatContextPath(path)
		}
		addLoadedSection("Context", formatCompactList(compact, false), strings.Join(expandedLines, "\n"))
	}

	if len(m.opts.Skills) > 0 {
		items := make([]loadedResource, len(m.opts.Skills))
		names := make([]string, len(m.opts.Skills))
		for i, skill := range m.opts.Skills {
			items[i] = m.loadedResourceFor(skill.Path, "skills")
			names[i] = skill.Name
		}
		list := formatScopeGroups(theme, collator, buildScopeGroups(items), formatDisplayPathItem, getShortPathItem)
		addLoadedSection("Skills", formatCompactList(names, true), list)
	}

	if len(m.promptTemplates) > 0 {
		items := make([]loadedResource, len(m.promptTemplates))
		names := make([]string, len(m.promptTemplates))
		byPath := make(map[string]string, len(m.promptTemplates))
		for i, template := range m.promptTemplates {
			items[i] = m.loadedResourceFor(template.FilePath, "prompts")
			names[i] = "/" + template.Name
			byPath[template.FilePath] = template.Name
		}
		formatTemplate := func(item loadedResource) string {
			if name, ok := byPath[item.path]; ok {
				return "/" + name
			}
			return formatDisplayPath(item.path)
		}
		list := formatScopeGroups(theme, collator, buildScopeGroups(items), formatTemplate, formatTemplate)
		addLoadedSection("Prompts", formatCompactList(names, true), list)
	}

	if extensions := m.loadedExtensionResources(); len(extensions) > 0 {
		list := formatScopeGroups(theme, collator, buildScopeGroups(extensions),
			func(item loadedResource) string { return formatExtensionDisplayPath(item.path) },
			func(item loadedResource) string {
				return formatExtensionDisplayPath(getShortPath(item.path, item.info))
			},
		)
		addLoadedSection("Extensions", formatCompactList(getCompactExtensionLabels(extensions), true), list)
	}

	if !m.opts.NoThemes {
		registry := tui.ActiveThemeRegistry()
		var themes []loadedResource
		var names []string
		for _, name := range registry.Names() {
			sourcePath := registry.PathOf(name)
			if sourcePath == "" {
				continue
			}
			item := m.loadedResourceFor(sourcePath, "themes")
			themes = append(themes, item)
			if name == "" {
				name = getCompactPathLabel(item.path, item.info)
			}
			names = append(names, name)
		}
		if len(themes) > 0 {
			list := formatScopeGroups(theme, collator, buildScopeGroups(themes), formatDisplayPathItem, getShortPathItem)
			addLoadedSection("Themes", formatCompactList(names, true), list)
		}
	}
}

func contextFilePaths(files []ContextFile) []string {
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}
	return paths
}

// loadedExtensionResources lists the loaded extensions with the SourceInfo
// stamped where each was collected, else the one the resource provenance
// records for its path.
func (m *InteractiveMode) loadedExtensionResources() []loadedResource {
	if m.newRunner == nil {
		return nil
	}
	sources := m.newRunner.ExtensionSources()
	out := make([]loadedResource, 0, len(sources))
	for _, source := range sources {
		if source.ResolvedPath == "" {
			continue
		}
		if info, ok := source.SourceInfo.(PiSourceInfo); ok {
			out = append(out, loadedResource{path: source.ResolvedPath, info: &info})
			continue
		}
		out = append(out, m.loadedResourceFor(source.ResolvedPath, "extensions"))
	}
	return out
}

// loadedResourceFor returns path with its SourceInfo, as upstream
// findSourceInfoForPath does: provenance an extension's resources_discover
// recorded for the path or an ancestor wins, then the configured resource
// provenance for the path or an ancestor, then the default for its location.
func (m *InteractiveMode) loadedResourceFor(path, kind string) loadedResource {
	for _, fromExtension := range []bool{true, false} {
		for current := path; ; {
			if info, ok := m.resourceSourceInfo[current]; ok && strings.HasPrefix(info.Source, "extension:") == fromExtension {
				resolved := SlashCommandCatalog{CWD: m.opts.CWD, AgentDir: m.opts.AgentDir, SourceInfo: map[string]ResourceSourceInfo{path: info}}.SourceInfoForPath(path, kind)
				return loadedResource{path: path, info: &resolved}
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}
	resolved := SlashCommandCatalog{CWD: m.opts.CWD, AgentDir: m.opts.AgentDir}.SourceInfoForPath(path, kind)
	return loadedResource{path: path, info: &resolved}
}

// formatDisplayPath is upstream formatDisplayPath: a path that starts with
// the home directory shows it as "~".
func formatDisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func formatDisplayPathItem(item loadedResource) string { return formatDisplayPath(item.path) }

func getShortPathItem(item loadedResource) string { return getShortPath(item.path, item.info) }

func formatExtensionDisplayPath(path string) string {
	result := formatDisplayPath(path)
	result = strings.TrimSuffix(result, "/index.ts")
	return strings.TrimSuffix(result, "/index.js")
}

func (m *InteractiveMode) formatContextPath(path string) string {
	cwd, err := filepath.Abs(m.opts.CWD)
	if err != nil {
		cwd = filepath.Clean(m.opts.CWD)
	}
	absolute := path
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(cwd, absolute)
	}
	absolute = filepath.Clean(absolute)
	if relative := GetCwdRelativePath(absolute, cwd); relative != "" {
		return relative
	}
	return formatDisplayPath(absolute)
}

func isPackageSource(info *PiSourceInfo) bool {
	if info == nil {
		return false
	}
	return strings.HasPrefix(info.Source, "npm:") || strings.HasPrefix(info.Source, "git:")
}

var (
	npmRootPattern  = regexp.MustCompile(`^(.*/node_modules)/(@?[^/]+(?:/[^/]+)?)$`)
	npmPathPattern  = regexp.MustCompile(`node_modules/(@?[^/]+(?:/[^/]+)?)/(.*)`)
	gitPathPattern  = regexp.MustCompile(`git/[^/]+/[^/]+/(.*)`)
	backslashSubber = strings.NewReplacer(`\`, "/")
)

// getShortPath is upstream getShortPath: a package resource shows relative to
// its package root, anything else as its display path.
func getShortPath(fullPath string, info *PiSourceInfo) string {
	normalizedFullPath := backslashSubber.Replace(fullPath)
	if info != nil && info.BaseDir != "" && isPackageSource(info) {
		normalizedBaseDir := backslashSubber.Replace(info.BaseDir)
		if match := npmRootPattern.FindStringSubmatch(normalizedBaseDir); match != nil && match[1] != "" && strings.HasPrefix(normalizedFullPath, match[1]+"/") {
			return posixRelative(normalizedBaseDir, normalizedFullPath)
		}
		base, baseErr := filepath.Abs(info.BaseDir)
		full, fullErr := filepath.Abs(fullPath)
		if baseErr == nil && fullErr == nil {
			if relative, err := filepath.Rel(base, full); err == nil && relative != "" && relative != "." && !strings.HasPrefix(relative, "..") && !filepath.IsAbs(relative) {
				return filepath.ToSlash(relative)
			}
		}
	}
	source := ""
	if info != nil {
		source = info.Source
	}
	if match := npmPathPattern.FindStringSubmatch(normalizedFullPath); match != nil && strings.HasPrefix(source, "npm:") {
		return match[2]
	}
	if match := gitPathPattern.FindStringSubmatch(normalizedFullPath); match != nil && strings.HasPrefix(source, "git:") {
		return match[1]
	}
	return formatDisplayPath(fullPath)
}

// posixRelative is Node's path.posix.relative for two absolute paths.
func posixRelative(from, to string) string {
	split := func(path string) []string {
		var parts []string
		for part := range strings.SplitSeq(path, "/") {
			if part != "" {
				parts = append(parts, part)
			}
		}
		return parts
	}
	fromParts, toParts := split(from), split(to)
	common := 0
	for common < len(fromParts) && common < len(toParts) && fromParts[common] == toParts[common] {
		common++
	}
	parts := make([]string, 0, len(fromParts)-common+len(toParts)-common)
	for range fromParts[common:] {
		parts = append(parts, "..")
	}
	parts = append(parts, toParts[common:]...)
	return strings.Join(parts, "/")
}

func getCompactPathLabel(path string, info *PiSourceInfo) string {
	shortPath := getShortPath(path, info)
	var last string
	for segment := range strings.SplitSeq(backslashSubber.Replace(shortPath), "/") {
		if segment != "" && segment != "~" {
			last = segment
		}
	}
	if last != "" {
		return last
	}
	return shortPath
}

func getCompactPackageSourceLabel(info *PiSourceInfo) string {
	source := ""
	if info != nil {
		source = info.Source
	}
	if name, ok := strings.CutPrefix(source, "npm:"); ok {
		if name == "" {
			return source
		}
		return name
	}
	if ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject}); err == nil && ref.Kind == sourceref.KindGit {
		if ref.GitPath == "" {
			return source
		}
		return ref.GitPath
	}
	return source
}

func getCompactExtensionLabel(path string, info *PiSourceInfo) string {
	if !isPackageSource(info) {
		return getCompactPathLabel(path, info)
	}
	sourceLabel := getCompactPackageSourceLabel(info)
	if sourceLabel == "" {
		return getCompactPathLabel(path, info)
	}
	shortPath := backslashSubber.Replace(getShortPath(path, info))
	packagePath := strings.TrimPrefix(shortPath, "extensions/")
	dir, base := "", packagePath
	if index := strings.LastIndex(packagePath, "/"); index >= 0 {
		dir, base = packagePath[:index], packagePath[index+1:]
	}
	name := base
	// Node's path.posix.parse keeps a leading dot as part of the name.
	if dot := strings.LastIndex(base, "."); dot > 0 {
		name = base[:dot]
	}
	if name == "index" {
		if dir == "" || dir == "." {
			return sourceLabel
		}
		return sourceLabel + ":" + dir
	}
	return sourceLabel + ":" + packagePath
}

func compactDisplayPathSegments(path string) []string {
	var segments []string
	for segment := range strings.SplitSeq(backslashSubber.Replace(formatDisplayPath(path)), "/") {
		if segment != "" && segment != "~" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// getCompactExtensionLabels labels a package extension by its package and
// entry, and any other extension by the shortest trailing path that no other
// non-package extension shares.
func getCompactExtensionLabels(extensions []loadedResource) []string {
	type pathSegments struct {
		path     string
		segments []string
	}
	var nonPackage []pathSegments
	for _, extension := range extensions {
		if isPackageSource(extension.info) {
			continue
		}
		segments := compactDisplayPathSegments(extension.path)
		if last := len(segments) - 1; len(segments) > 1 && (segments[last] == "index.ts" || segments[last] == "index.js") {
			segments = segments[:last]
		}
		nonPackage = append(nonPackage, pathSegments{path: extension.path, segments: segments})
	}
	labels := make([]string, len(extensions))
	for i, extension := range extensions {
		if isPackageSource(extension.info) {
			labels[i] = getCompactExtensionLabel(extension.path, extension.info)
			continue
		}
		index := slices.IndexFunc(nonPackage, func(item pathSegments) bool { return item.path == extension.path })
		if index < 0 {
			labels[i] = getCompactPathLabel(extension.path, extension.info)
			continue
		}
		segments := nonPackage[index].segments
		if len(segments) == 0 {
			labels[i] = getCompactPathLabel(extension.path, nil)
			continue
		}
		labels[i] = strings.Join(segments, "/")
		for count := 1; count <= len(segments); count++ {
			candidate := strings.Join(segments[len(segments)-count:], "/")
			unique := true
			for other, item := range nonPackage {
				if other == index {
					continue
				}
				start := max(0, len(item.segments)-count)
				if strings.Join(item.segments[start:], "/") == candidate {
					unique = false
					break
				}
			}
			if unique {
				labels[i] = candidate
				break
			}
		}
	}
	return labels
}

// scopeGroup is upstream getScopeGroup.
func scopeGroup(info *PiSourceInfo) string {
	source, scope := "local", "project"
	if info != nil {
		if info.Source != "" {
			source = info.Source
		}
		if info.Scope != "" {
			scope = info.Scope
		}
	}
	switch {
	case source == "cli" || scope == "temporary":
		return "path"
	case scope == "user":
		return "user"
	case scope == "project":
		return "project"
	default:
		return "path"
	}
}

// buildScopeGroups is upstream buildScopeGroups: project, user, then path
// groups, each with plain paths and per-source package lists.
func buildScopeGroups(items []loadedResource) []loadedScopeGroup {
	groups := map[string]*loadedScopeGroup{}
	for _, scope := range []string{"project", "user", "path"} {
		groups[scope] = &loadedScopeGroup{scope: scope, packages: map[string][]loadedResource{}}
	}
	for _, item := range items {
		group := groups[scopeGroup(item.info)]
		if isPackageSource(item.info) {
			group.packages[item.info.Source] = append(group.packages[item.info.Source], item)
			continue
		}
		group.paths = append(group.paths, item)
	}
	var out []loadedScopeGroup
	for _, scope := range []string{"project", "user", "path"} {
		if group := groups[scope]; len(group.paths) > 0 || len(group.packages) > 0 {
			out = append(out, *group)
		}
	}
	return out
}

// formatScopeGroups is upstream formatScopeGroups.
func formatScopeGroups(theme *tui.Theme, collator *collate.Collator, groups []loadedScopeGroup, formatPath, formatPackagePath func(loadedResource) string) string {
	byPath := func(a, b loadedResource) int { return collator.CompareString(a.path, b.path) }
	var lines []string
	for _, group := range groups {
		lines = append(lines, "  "+theme.FgText("accent", group.scope))
		paths := slices.Clone(group.paths)
		slices.SortStableFunc(paths, byPath)
		for _, item := range paths {
			lines = append(lines, theme.FgText("dim", "    "+formatPath(item)))
		}
		sources := make([]string, 0, len(group.packages))
		for source := range group.packages {
			sources = append(sources, source)
		}
		slices.SortStableFunc(sources, collator.CompareString)
		for _, source := range sources {
			lines = append(lines, "    "+theme.FgText("mdLink", source))
			items := slices.Clone(group.packages[source])
			slices.SortStableFunc(items, byPath)
			for _, item := range items {
				lines = append(lines, theme.FgText("dim", "      "+formatPackagePath(item)))
			}
		}
	}
	return strings.Join(lines, "\n")
}
