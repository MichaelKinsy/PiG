package mcp_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Pi packages/mcp/test/naming.test.ts "createMcpToolName": packages/mcp/src/naming.ts createMcpToolName.
// mutation-checked: a sanitizing character class that keeps `-` or `.`, a limit other than 64, a hash taken over the sanitized names, a 7- or 9-character hash, and a suffix that ignores isTaken fail it.
func TestCreateMcpToolName(t *testing.T) {
	t.Run("joins server and tool and replaces characters outside [A-Za-z0-9_]", func(t *testing.T) {
		if got := mcp.CreateMcpToolName("docs", "search", nil); got != "mcp__docs__search" {
			t.Errorf("CreateMcpToolName(docs, search) = %q", got)
		}
		if got := mcp.CreateMcpToolName("my-server", "get.item/v2", nil); got != "mcp__my_server__get_item_v2" {
			t.Errorf("CreateMcpToolName(my-server, get.item/v2) = %q", got)
		}
	})

	t.Run("shortens names over 64 characters with a hash of server and tool", func(t *testing.T) {
		long := mcp.CreateMcpToolName("server", strings.Repeat("x", 100), nil)
		if len(long) != 64 {
			t.Errorf("len(%q) = %d, want 64", long, len(long))
		}
		if !regexp.MustCompile(`^mcp__server__x+_[0-9a-f]{8}$`).MatchString(long) {
			t.Errorf("%q does not match mcp__server__x+_<8 hex>", long)
		}
		// Names that share their first 55 characters still differ.
		if other := mcp.CreateMcpToolName("server", strings.Repeat("x", 100)+"y", nil); other == long {
			t.Errorf("names sharing their first 55 characters collide: %q", other)
		}
		// The hash covers the unsanitized names, and is stable.
		if again := mcp.CreateMcpToolName("server", strings.Repeat("x", 100), nil); again != long {
			t.Errorf("the name is not stable: %q then %q", long, again)
		}
		if a, b := mcp.CreateMcpToolName("ser-ver", strings.Repeat("x", 100), nil), mcp.CreateMcpToolName("ser_ver", strings.Repeat("x", 100), nil); a == b {
			t.Errorf("servers that differ in - and _ collide: %q", a)
		}
	})

	t.Run("adds the hash suffix when the sanitized name is taken", func(t *testing.T) {
		taken := mcp.CreateMcpToolName("s", "a_b", nil)
		if taken != "mcp__s__a_b" {
			t.Fatalf("CreateMcpToolName(s, a_b) = %q", taken)
		}
		isTaken := func(name string) bool { return name == taken }
		second := mcp.CreateMcpToolName("s", "a-b", isTaken)
		if !regexp.MustCompile(`^mcp__s__a_b_[0-9a-f]{8}$`).MatchString(second) {
			t.Errorf("CreateMcpToolName(s, a-b) with a taken name = %q", second)
		}
		if other := mcp.CreateMcpToolName("s", "a_b", isTaken); other == second {
			t.Errorf("the hash does not cover the unsanitized tool: %q", other)
		}
	})
}

// Pi packages/mcp/test/naming.test.ts "mcpNamespace": packages/mcp/src/naming.ts mcpNamespace.
// mutation-checked: a namespace that keeps `-`, drops the `mcp__` prefix, or replaces `_` fails it.
func TestMcpNamespace(t *testing.T) {
	if got := mcp.McpNamespace("docs"); got != "mcp__docs" {
		t.Errorf("McpNamespace(docs) = %q", got)
	}
	if got := mcp.McpNamespace("my-server"); got != "mcp__my_server" {
		t.Errorf("McpNamespace(my-server) = %q", got)
	}
	if mcp.McpNamespace("my-server") != mcp.McpNamespace("my_server") {
		t.Error("server names that differ only in - and _ do not share a namespace")
	}
	if !strings.HasPrefix(mcp.CreateMcpToolName("my-server", "search", nil), mcp.McpNamespace("my-server")+"__") {
		t.Error("a server's tool names do not start with its namespace")
	}
}

// A character outside the BMP is two UTF-16 code units, and the JavaScript regular expression replaces each with an underscore.
// mutation-checked: one underscore for an astral character fails it.
func TestCreateMcpToolNameCountsUTF16CodeUnits(t *testing.T) {
	if got := mcp.CreateMcpToolName("s", "😀", nil); got != "mcp__s____" {
		t.Errorf("CreateMcpToolName(s, 😀) = %q, want mcp__s____", got)
	}
	if got := mcp.CreateMcpToolName("s", "é", nil); got != "mcp__s___" {
		t.Errorf("CreateMcpToolName(s, é) = %q, want mcp__s___", got)
	}
}
