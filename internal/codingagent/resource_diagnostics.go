package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts
// formatDiagnostics and formatPathWithSource.

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// findSourceInfoForPath is upstream findSourceInfoForPath: the path's own entry, else the entry of the nearest
// ancestor reached by cutting at the last "/" of the text, without normalizing it.
func findSourceInfoForPath(path string, infos map[string]*PiSourceInfo) *PiSourceInfo {
	if exact := infos[path]; exact != nil {
		return exact
	}
	current := path
	for strings.Contains(current, "/") {
		current = current[:strings.LastIndex(current, "/")]
		if parent := infos[current]; parent != nil {
			return parent
		}
	}
	return nil
}

func formatPathWithSource(path string, infos map[string]*PiSourceInfo) string {
	info := findSourceInfoForPath(path, infos)
	if info == nil {
		return formatDisplayPath(path)
	}
	source, scope := info.Source, info.Scope
	if source == "" {
		source = "local"
	}
	if scope == "" {
		scope = "project"
	}
	label, scopeLabel := source, scope
	switch source {
	case "local":
		label, scopeLabel = "path", ""
		switch scope {
		case "user", "project":
			label = scope
		case "temporary":
			scopeLabel = "temp"
		}
	case "cli":
		label, scopeLabel = "path", ""
		if scope == "temporary" {
			scopeLabel = "temp"
		}
	default:
		if scope == "temporary" {
			scopeLabel = "temp"
		} else if scope != "user" && scope != "project" {
			scopeLabel = ""
		}
	}
	if scopeLabel != "" {
		label += " (" + scopeLabel + ")"
	}
	return label + " " + getShortPath(path, info)
}

func formatResourceDiagnostics(diagnostics []extension.ResourceDiagnostic, infos map[string]*PiSourceInfo) string {
	theme := tui.ActiveTheme()
	var names []string
	groups := make(map[string][]*extension.ResourceCollision)
	var others []extension.ResourceDiagnostic
	for _, d := range diagnostics {
		if d.Type != extension.DiagnosticCollision || d.Collision == nil {
			others = append(others, d)
			continue
		}
		name := d.Collision.Name
		if _, ok := groups[name]; !ok {
			names = append(names, name)
		}
		groups[name] = append(groups[name], d.Collision)
	}
	var lines []string
	for _, name := range names {
		group := groups[name]
		lines = append(lines, theme.Fg("warning", fmt.Sprintf(`  "%s" collision:`, name)))
		lines = append(lines, theme.Fg("dim", "    "+theme.Fg("success", "✓")+" "+formatPathWithSource(group[0].WinnerPath, infos)))
		for _, collision := range group {
			lines = append(lines, theme.Fg("dim", "    "+theme.Fg("warning", "✗")+" "+formatPathWithSource(collision.LoserPath, infos)+" (skipped)"))
		}
	}
	for _, d := range others {
		color := "warning"
		if d.Type == extension.DiagnosticError {
			color = "error"
		}
		indent := "  "
		if d.Path != "" {
			lines = append(lines, theme.Fg(color, indent+formatPathWithSource(d.Path, infos)))
			indent += "  "
		}
		lines = append(lines, theme.Fg(color, indent+d.Message))
	}
	return strings.Join(lines, "\n")
}
