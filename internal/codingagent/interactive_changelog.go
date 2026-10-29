package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/text"
	"github.com/MichaelKinsy/PiG/tui"
)

// changelogVersionRE is /##\s+\[?(\d+\.\d+\.\d+)\]?/ (interactive-mode.ts:858), unanchored.
var changelogVersionRE = lazyregexp.New(`##[` + jsSpace + `]+\[?(\d+\.\d+\.\d+)\]?`)

// extensionChangelogUpdate is upstream's ExtensionChangelogUpdate: one package's changelog entries newer than the last version seen, headed by the package name.
type extensionChangelogUpdate struct {
	Name     string
	Version  string
	Markdown string
}

// extensionChangelogHeadingRE is /^##\s+/, the heading prefix getExtensionChangelogUpdates replaces with one that names the package.
var extensionChangelogHeadingRE = lazyregexp.New(`^##[` + jsSpace + `]+`)

// getExtensionChangelogUpdates is interactive-mode.ts getExtensionChangelogUpdates over the loaded extensions' source infos: nothing for a session that already has messages;
// for each package that loads an extension and names a changelog, once, the entries newer than the version recorded for it. A package seen for the first time records its
// version and shows nothing; one with new entries records its version and yields an update named after package.json's name, else its source.
func getExtensionChangelogUpdates(sm *SettingsManager, sourceInfos []PiSourceInfo, hasMessages bool) []extensionChangelogUpdate {
	if hasMessages {
		return nil
	}
	seenKeys := make(map[string]struct{})
	var updates []extensionChangelogUpdate
	for _, sourceInfo := range sourceInfos {
		if sourceInfo.Origin != "package" || sourceInfo.BaseDir == "" || sourceInfo.ChangelogPath == "" {
			continue
		}
		key := source.GetSourcePackageKey(sourceInfo.Source, source.SourceScope(sourceInfo.Scope))
		if _, seen := seenKeys[key]; seen {
			continue
		}
		seenKeys[key] = struct{}{}
		pkgName, pkgVersion := readPackageJson(sourceInfo.BaseDir)
		if pkgVersion == "" {
			continue
		}
		lastVersion, _ := sm.GetExtensionChangelogVersion(key)
		if lastVersion == "" {
			// Fresh install: record the version, show nothing.
			_ = sm.SetExtensionChangelogVersion(key, pkgVersion)
			continue
		}
		newEntries := GetNewEntries(parseChangelogFile(sourceInfo.ChangelogPath), lastVersion)
		if len(newEntries) == 0 {
			continue
		}
		_ = sm.SetExtensionChangelogVersion(key, pkgVersion)
		name := pkgName
		if name == "" {
			name = sourceInfo.Source
		}
		// Prefix each "## [x.y.z]" heading with the package name so entries render like Pi's own.
		parts := make([]string, len(newEntries))
		for i, entry := range newEntries {
			parts[i] = extensionChangelogHeadingRE.ReplaceAllLiteralString(entry.Content, "## "+name+" ")
		}
		updates = append(updates, extensionChangelogUpdate{Name: name, Version: pkgVersion, Markdown: jsstring.Trim(strings.Join(parts, "\n\n"))})
	}
	return updates
}

// parseChangelogFile is parseChangelog(changelogPath) (utils/changelog.ts): the entries of the file, none when it cannot be read.
func parseChangelogFile(path string) []ChangelogEntry {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return ParseChangelog(string(content))
}

// readPackageJson is interactive-mode.ts readPackageJson: the string name and version of packageRoot/package.json, with a byte order mark stripped. A field that is missing or not a
// string reads as empty, and an unreadable or malformed file as both empty.
func readPackageJson(packageRoot string) (name, version string) {
	content, err := os.ReadFile(filepath.Join(packageRoot, "package.json"))
	if err != nil {
		return "", ""
	}
	var pkg struct {
		Name    any `json:"name"`
		Version any `json:"version"`
	}
	if err := json.Unmarshal(text.StripBomBytes(content), &pkg); err != nil {
		return "", ""
	}
	name, _ = pkg.Name.(string)
	version, _ = pkg.Version.(string)
	return name, version
}

// getExtensionChangelogUpdates is the update list for this run: the source infos of every loaded extension, and no updates for a resumed session.
func (m *InteractiveMode) getExtensionChangelogUpdates() []extensionChangelogUpdate {
	if m.opts.SettingsManager == nil || m.newRunner == nil {
		return nil
	}
	sources := m.newRunner.ExtensionSources()
	infos := make([]PiSourceInfo, len(sources))
	for i, extensionSource := range sources {
		infos[i] = extensionSource.SourceInfo
	}
	return getExtensionChangelogUpdates(m.opts.SettingsManager, infos, m.opts.ResumePath != "")
}

// showStartupChangelog is showStartupNoticesIfNeeded's changelog block (interactive-mode.ts) for the entries newer than the last version seen, in file order with their links
// normalized (getChangelogForDisplay), and the extension updates: a spacer when the chat is not empty, a border, Pi's own changelog as one condensed line (collapseChangelog) or the
// title and padded Markdown, a spacer and the extension updates the same two ways when both are shown, and a closing border.
func (m *InteractiveMode) showStartupChangelog(newEntries []ChangelogEntry, extensionUpdates []extensionChangelogUpdate) {
	if len(newEntries) == 0 && len(extensionUpdates) == 0 {
		return
	}
	collapse := m.opts.SettingsManager != nil && m.opts.SettingsManager.GetCollapseChangelog()
	if len(m.chatContainer.Children()) > 0 {
		m.chatContainer.Add(tui.NewSpacer(1))
	}
	m.chatContainer.Add(tui.NewDynamicBorder())
	if len(newEntries) > 0 {
		parts := make([]string, len(newEntries))
		for i, entry := range newEntries {
			parts[i] = NormalizeChangelogLinks(entry.Content, entry.Version())
		}
		changelog := strings.Join(parts, "\n\n")
		if collapse {
			latestVersion := m.opts.AppVersion
			if match := changelogVersionRE.FindStringSubmatch(changelog); match != nil {
				latestVersion = match[1]
			}
			m.chatContainer.Add(tui.NewPaddedText("Updated to v"+latestVersion+". Use \x1b[1m/changelog\x1b[22m to view full changelog.", 1, 0, nil))
		} else {
			m.chatContainer.Add(tui.NewThemedText(func() string {
				return "\x1b[1m" + tui.ActiveTheme().Fg("accent", "What's New") + tui.SGRBoldDimReset
			}, 1, 0))
			m.chatContainer.Add(tui.NewSpacer(1))
			m.chatContainer.Add(tui.NewMarkdownWithOptions(jsstring.Trim(changelog), 1, 0, m.markdownThemeWithSettings(), nil, nil))
			m.chatContainer.Add(tui.NewSpacer(1))
		}
	}
	if len(extensionUpdates) > 0 {
		if len(newEntries) > 0 {
			m.chatContainer.Add(tui.NewSpacer(1))
		}
		if collapse {
			lines := make([]string, len(extensionUpdates))
			for i, update := range extensionUpdates {
				lines[i] = "Updated " + update.Name + " to v" + update.Version + "."
			}
			m.chatContainer.Add(tui.NewPaddedText(strings.Join(lines, "\n"), 1, 0, nil))
		} else {
			m.chatContainer.Add(tui.NewThemedText(func() string {
				return "\x1b[1m" + tui.ActiveTheme().Fg("accent", "Extension Updates") + tui.SGRBoldDimReset
			}, 1, 0))
			m.chatContainer.Add(tui.NewSpacer(1))
			markdowns := make([]string, len(extensionUpdates))
			for i, update := range extensionUpdates {
				markdowns[i] = update.Markdown
			}
			m.chatContainer.Add(tui.NewMarkdownWithOptions(strings.Join(markdowns, "\n\n"), 1, 0, m.markdownThemeWithSettings(), nil, nil))
			m.chatContainer.Add(tui.NewSpacer(1))
		}
	}
	m.chatContainer.Add(tui.NewDynamicBorder())
}

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (handleChangelogCommand).
// handleChangelogCommand appends every released entry oldest-first, with a separate title and borders. CollapseChangelog controls startup notices only.
func (m *InteractiveMode) handleChangelogCommand() {
	body := tui.NewPaddedBox(1, 1, nil)
	body.AddChild(tui.NewMarkdownWithOptions(changelogMarkdown(ParseChangelog(bundledChangelog())), 0, 0, m.markdownThemeWithSettings(), nil, nil))
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(tui.NewDynamicBorder())
	m.chatContainer.Add(tui.NewThemedText(func() string {
		return "\x1b[1m" + tui.ActiveTheme().Fg("accent", "What's New") + tui.SGRBoldDimReset
	}, 1, 0))
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(body)
	m.chatContainer.Add(tui.NewDynamicBorder())
	m.requestRender()
}
