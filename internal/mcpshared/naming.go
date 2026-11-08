package mcpshared

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Ports packages/mcp/src/naming.ts (Pi 76c040a2a, moved from coding-agent's extensions/mcp/tools.ts and core/mcp-servers.ts).
//
// Names of MCP tools and of their servers' namespaces as models and code mode scripts see them. Applications that name tools the same way rank
// and group them the same way in tool search and code mode.

// maxToolNameLength: provider tool names are limited to 64 characters of `[A-Za-z0-9_-]`.
const maxToolNameLength = 64

var toolNameUnsafe = lazyregexp.New(`[^A-Za-z0-9_]`)

// sanitizeToolName replaces every character outside `[A-Za-z0-9_]` with an underscore per UTF-16 code unit, as the JavaScript regular
// expression does.
func sanitizeToolName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x80 && !toolNameUnsafe.MatchString(string(r)) {
			b.WriteRune(r)
			continue
		}
		b.WriteString(strings.Repeat("_", utf16.RuneLen(r)))
	}
	return b.String()
}

// CreateMcpToolName is `mcp__<server>__<tool>`, sanitized and shortened with a hash suffix when too long. Like Codex, everything but
// `[A-Za-z0-9_]` becomes `_`, so the name is also the identifier code mode scripts call it by. isTaken (nil: nothing is taken) reports names used
// by a different MCP tool: sanitizing can map two tools to one name (`a-b` and `a_b`), which then get the hash suffix.
func CreateMcpToolName(server, tool string, isTaken func(string) bool) string {
	name := sanitizeToolName("mcp__" + server + "__" + tool)
	//portlint:allow utf16units the name is ASCII after sanitizing, which turns every UTF-16 code unit outside [A-Za-z0-9_] into one underscore, so bytes and code units agree (naming.ts createMcpToolName)
	if len(name) <= maxToolNameLength && (isTaken == nil || !isTaken(name)) {
		return name
	}
	digest := sha256.Sum256([]byte(server + "\x00" + tool))
	//portlint:allow utf16units hex digits are ASCII (naming.ts createMcpToolName)
	hash := hex.EncodeToString(digest[:])[:8]
	//portlint:allow utf16units the name is ASCII after sanitizing, which turns every UTF-16 code unit outside [A-Za-z0-9_] into one underscore, so bytes and code units agree (naming.ts createMcpToolName)
	return name[:min(len(name), maxToolNameLength-len(hash)-1)] + "_" + hash
}

// McpNamespace is the namespace of a server's tools: `mcp__<server>` with `-` replaced by `_`, like the tool names. Server names that differ only
// in `-` and `_` share a namespace, so a configuration must not have both.
func McpNamespace(server string) string {
	return "mcp__" + strings.ReplaceAll(server, "-", "_")
}
