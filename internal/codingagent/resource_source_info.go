package codingagent

import (
	"path/filepath"
	"strings"
)

// ResourceSourceInfo tracks where a loaded resource came from.
// Mirrors upstream PathMetadata / SourceInfo flow used by resource-loader.ts
// and interactive-mode.ts to annotate startup context and collision
// diagnostics with package origin + scope.
type ResourceSourceInfo struct {
	Path         string
	ResourceType string // "extensions" | "skills" | "prompts" | "themes"
	Enabled      bool
	Scope        string // "user" | "project"
	Origin       string // "package" | "top-level"
	Source       string // package source string or "local"
	BaseDir      string // package root for package-relative shortening
}

// DisplayName derives the logical resource name used for collision grouping.
func (i ResourceSourceInfo) DisplayName() string {
	switch i.ResourceType {
	case "skills", "extensions":
		return filepath.Base(i.Path)
	case "prompts", "themes":
		base := filepath.Base(i.Path)
		return strings.TrimSuffix(base, filepath.Ext(base))
	default:
		return filepath.Base(i.Path)
	}
}
