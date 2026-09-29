package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (getBuiltInCommandConflictDiagnostics and showLoadedResources).
// Ports packages/coding-agent/src/core/resource-loader.ts (loadThemes and dedupeThemes).
// Ports packages/coding-agent/src/modes/interactive/theme/theme.ts (getCustomThemeInfos).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// extensionDiagnostics lists, in Pi's order, the extension load errors, the
// runner's command diagnostics, the built-in command conflicts and the
// shortcut diagnostics.
func (m *InteractiveMode) extensionDiagnostics() []extension.ResourceDiagnostic {
	var out []extension.ResourceDiagnostic
	if host := m.opts.SubprocessHost; host != nil {
		issues := host.LoadErrors()
		if report := host.LastReloadReport(); report != nil {
			issues = report.Issues
		}
		for _, issue := range issues {
			out = append(out, extensionLoadIssueDiagnostic(issue))
		}
	}
	for _, conflict := range m.extensionConflicts {
		out = append(out, extension.ResourceDiagnostic{Type: extension.DiagnosticError, Message: conflict.Message, Path: conflict.Path})
	}
	if m.newRunner != nil {
		out = append(out, m.newRunner.CommandDiagnostics()...)
		out = append(out, m.builtInCommandConflictDiagnostics()...)
		out = append(out, m.newRunner.ShortcutDiagnostics()...)
	}
	return out
}

// extensionLoadIssueDiagnostic separates a reload issue's path from its complete loader diagnostic.
func extensionLoadIssueDiagnostic(issue string) extension.ResourceDiagnostic {
	for _, messagePrefix := range []string{"Failed to load extension: ", "Extension does not export a valid factory function: "} {
		if path, message, ok := strings.Cut(issue, ": "+messagePrefix); ok {
			return extension.ResourceDiagnostic{Type: extension.DiagnosticError, Message: messagePrefix + message, Path: path}
		}
	}
	return extension.ResourceDiagnostic{Type: extension.DiagnosticError, Message: issue}
}

func (m *InteractiveMode) builtInCommandConflictDiagnostics() []extension.ResourceDiagnostic {
	builtin := make(map[string]struct{})
	for _, command := range BuiltinSlashCommands() {
		builtin[command.Name] = struct{}{}
	}
	var out []extension.ResourceDiagnostic
	for _, command := range m.newRunner.Commands() {
		if _, ok := builtin[command.Name]; !ok {
			continue
		}
		message := fmt.Sprintf("Extension command '/%s' conflicts with built-in interactive command. Available as '/%s'.", command.Name, command.InvocationName)
		if command.InvocationName == command.Name {
			message = fmt.Sprintf("Extension command '/%s' conflicts with built-in interactive command. Skipping in autocomplete.", command.Name)
		}
		out = append(out, extension.ResourceDiagnostic{Type: extension.DiagnosticWarning, Message: message, Path: sourceInfoPath(command.SourceInfo)})
	}
	return out
}

func sourceInfoPath(info extension.SourceInfo) string {
	switch typed := info.(type) {
	case PiSourceInfo:
		return typed.Path
	case *PiSourceInfo:
		if typed != nil {
			return typed.Path
		}
	}
	return ""
}

// loadThemes replaces the registered theme set with the resolved resources, including explicit paths under --no-themes.
// Valid named themes in the custom themes directory stay selectable, as Pi getAvailableThemesWithPaths and loadThemeJson find them without registration.
func (m *InteractiveMode) loadThemes() {
	registry := tui.NewThemeRegistry()
	m.loadedThemes, m.themeDiagnostics = loadThemeResources(registry, m.opts.ThemePaths)
	if m.opts.AgentDir != "" {
		addCustomDirectoryThemes(registry, filepath.Join(m.opts.AgentDir, "themes"))
	}
	tui.SetThemeRegistry(registry)
}

// addCustomDirectoryThemes mirrors Pi getCustomThemeInfos: unreadable and invalid files are ignored, and built-in or registered names keep precedence.
func addCustomDirectoryThemes(registry *tui.ThemeRegistry, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		theme, err := tui.LoadThemeFile(path)
		if err != nil || theme.Name == "" || registry.Get(theme.Name) != nil {
			continue
		}
		registry.AddFile(theme, path)
	}
}

// nodeThemeFSError formats a theme file-system failure as the Node fs call in Pi's loader reports it.
func nodeThemeFSError(err error, syscallName, path string) string {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		if pathErr.Op == "read" {
			return tools.NodeFSError(pathErr, "read", "")
		}
		return tools.NodeFSError(pathErr, syscallName, path)
	}
	return err.Error()
}

type loadedTheme struct {
	theme *tui.Theme
	path  string
}

// loadThemeResources registers and returns the first theme of each name with ordered load warnings and name collisions.
// Loaded empty-name themes remain resources even though the registry has no key for them.
func loadThemeResources(registry *tui.ThemeRegistry, paths []string) ([]loadedTheme, []extension.ResourceDiagnostic) {
	var themes []loadedTheme
	var diagnostics []extension.ResourceDiagnostic
	warn := func(message, path string) {
		diagnostics = append(diagnostics, extension.ResourceDiagnostic{Type: extension.DiagnosticWarning, Message: message, Path: path})
	}
	loadFile := func(path string) {
		theme, err := tui.LoadThemeFile(path)
		if err != nil {
			warn(nodeThemeFSError(err, "open", path), path)
			return
		}
		themes = append(themes, loadedTheme{theme, path})
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		switch {
		case err != nil:
			// Pi existsSync is false for every stat failure (resource-loader.ts:886-890).
			warn("theme path does not exist", path)
		case info.IsDir():
			entries, err := os.ReadDir(path)
			if err != nil {
				warn(nodeThemeFSError(err, "scandir", path), path)
				continue
			}
			for _, entry := range entries {
				file := filepath.Join(path, entry.Name())
				if stat, err := os.Stat(file); err == nil && stat.Mode().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
					loadFile(file)
				}
			}
		case info.Mode().IsRegular() && strings.HasSuffix(path, ".json"):
			loadFile(path)
		default:
			warn("theme path is not a json file", path)
		}
	}
	winners := make(map[string]string)
	var unique []loadedTheme
	for _, item := range themes {
		name := item.theme.Name
		if winner, seen := winners[name]; seen {
			diagnostics = append(diagnostics, extension.ResourceDiagnostic{
				Type: extension.DiagnosticCollision, Message: `name "` + name + `" collision`, Path: item.path,
				Collision: &extension.ResourceCollision{ResourceType: "theme", Name: name, WinnerPath: winner, LoserPath: item.path},
			})
			continue
		}
		winners[name] = item.path
		unique = append(unique, item)
		registry.AddFile(item.theme, item.path)
	}
	return unique, diagnostics
}
