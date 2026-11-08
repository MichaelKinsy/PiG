package contract

import (
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// Workspace panel protocol (protocol.ts): read-only views of a conversation's working directory, always resolved inside
// that conversation's execution environment by its worker.

// The text, read and download bounds.
const (
	WorkspaceTextLimit     = 1024 * 1024
	WorkspaceReadChunk     = 1024 * 1024
	WorkspaceDownloadLimit = 25 * 1024 * 1024
)

// WorkspaceSearchText is search text: 1-200 characters on one line.
func WorkspaceSearchText(value string) (string, bool) {
	if p2runtime.JSTrim(value) == "" || p2runtime.JSLength(value) > 200 || strings.ContainsAny(value, "\x00\r\n") {
		return "", false
	}
	return value, true
}

// WorkspaceGlobs are VS Code-style file globs, comma-separated: `*.ts, src/`, `*.{ts,tsx}`. Braces expand; a pattern
// matches at any depth unless it starts with `./` or `/`. It returns normalized git glob patterns (`**\/*.ts`); callers
// also match everything below a matching folder. Not ok when invalid: control characters, backslashes, `..` segments,
// unbalanced braces, over 500 characters or 32 expanded patterns. "" is no patterns.
func WorkspaceGlobs(value string) ([]string, bool) {
	if p2runtime.JSLength(value) > 500 || strings.Contains(value, `\`) {
		return nil, false
	}
	for _, character := range value {
		if character < ' ' {
			return nil, false
		}
	}
	var patterns []string
	// expand expands the first brace group of each pattern until none is left.
	var expand func(pattern string) bool
	expand = func(pattern string) bool {
		open := strings.Index(pattern, "{")
		if open < 0 {
			if strings.Contains(pattern, "}") {
				return false
			}
			patterns = append(patterns, pattern)
			return len(patterns) <= 32
		}
		var parts []string
		depth := 0
		start := open + 1
		for index := open; index < len(pattern); index++ {
			switch character := pattern[index]; {
			case character == '{':
				depth++
			case character == ',' && depth == 1:
				parts = append(parts, pattern[start:index])
				start = index + 1
			case character == '}':
				depth--
				if depth == 0 {
					parts = append(parts, pattern[start:index])
					rest := pattern[index+1:]
					for _, part := range parts {
						if !expand(pattern[:open] + part + rest) {
							return false
						}
					}
					return true
				}
			}
		}
		return false
	}
	// Commas inside braces belong to the brace group, not the list.
	depth := 0
	start := 0
	for index := 0; index <= len(value); index++ {
		var character byte
		if index < len(value) {
			character = value[index]
		}
		switch {
		case index < len(value) && character == '{':
			depth++
		case index < len(value) && character == '}':
			depth--
		case index == len(value) || (character == ',' && depth == 0):
			pattern := p2runtime.JSTrim(value[start:index])
			start = index + 1
			if pattern != "" && !expand(pattern) {
				return nil, false
			}
		}
	}
	normalized := []string{}
	seen := map[string]bool{}
	for _, pattern := range patterns {
		anchored := strings.HasPrefix(pattern, "/") || strings.HasPrefix(pattern, "./")
		var segments []string
		for _, segment := range strings.Split(pattern, "/") {
			if segment != "" && segment != "." {
				segments = append(segments, segment)
			}
		}
		for _, segment := range segments {
			if segment == ".." {
				return nil, false
			}
		}
		if len(segments) == 0 {
			continue
		}
		glob := strings.Join(segments, "/")
		if !anchored && glob != "**" && !strings.HasPrefix(glob, "**/") {
			glob = "**/" + glob
		}
		if !seen[glob] {
			seen[glob] = true
			normalized = append(normalized, glob)
		}
	}
	return normalized, true
}
