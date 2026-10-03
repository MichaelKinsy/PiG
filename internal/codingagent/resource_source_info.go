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

// DisplayName derives the path-based resource name used for collision grouping. A skill entry file uses its parent directory, matching Pi's default skill name.
func (i ResourceSourceInfo) DisplayName() string {
	switch i.ResourceType {
	case "skills":
		path := i.Path
		if strings.HasSuffix(path, ".md") {
			path = filepath.Dir(path)
		}
		return filepath.Base(path)
	case "extensions":
		return filepath.Base(i.Path)
	case "prompts", "themes":
		base := filepath.Base(i.Path)
		return strings.TrimSuffix(base, filepath.Ext(base))
	default:
		return filepath.Base(i.Path)
	}
}

// BuiltinPathPrefix is the prefix of built-in tool and extension paths, such as `builtin:read` or `builtin:mcp`.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/source-info.ts:14-15.
const BuiltinPathPrefix = "builtin:"

// SyntheticPathSource is the source of a path that names no file: "builtin" for `builtin:<name>`, or the prefix of an angle-bracket path such as "inline" for `<inline:name>`. It is empty for file paths.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/source-info.ts:17-25.
func SyntheticPathSource(path string) string {
	if strings.HasPrefix(path, BuiltinPathPrefix) {
		return "builtin"
	}
	if strings.HasPrefix(path, "<") && strings.HasSuffix(path, ">") {
		if source, _, _ := strings.Cut(path[1:len(path)-1], ":"); source != "" {
			return source
		}
		return "temporary"
	}
	return ""
}

// IsSyntheticPath reports whether path names no file: `builtin:<name>` or an angle-bracket path.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/source-info.ts:27-29.
func IsSyntheticPath(path string) bool {
	return strings.HasPrefix(path, BuiltinPathPrefix) || strings.HasPrefix(path, "<")
}
