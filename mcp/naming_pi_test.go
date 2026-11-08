package mcp_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/naming.test.ts (Pi 76c040a2a).

func TestCreateMcpToolNameJoinsServerAndToolAndReplacesCharactersOutsideTheAllowedSet(t *testing.T) {
	if got := mcp.CreateMcpToolName("docs", "search", nil); got != "mcp__docs__search" {
		t.Fatalf("name = %q", got)
	}
	if got := mcp.CreateMcpToolName("my-server", "get.item/v2", nil); got != "mcp__my_server__get_item_v2" {
		t.Fatalf("name = %q", got)
	}
}

func TestCreateMcpToolNameShortensNamesOver64CharactersWithAHashOfServerAndTool(t *testing.T) {
	long := mcp.CreateMcpToolName("server", strings.Repeat("x", 100), nil)
	if len(long) != 64 || !regexp.MustCompile(`^mcp__server__x+_[0-9a-f]{8}$`).MatchString(long) {
		t.Fatalf("name = %q", long)
	}
	// Names that share their first 55 characters still differ.
	if mcp.CreateMcpToolName("server", strings.Repeat("x", 100)+"y", nil) == long {
		t.Fatal("names sharing 55 characters are equal")
	}
	// The hash covers the unsanitized names, and is stable.
	if mcp.CreateMcpToolName("server", strings.Repeat("x", 100), nil) != long {
		t.Fatal("the name is not stable")
	}
	if mcp.CreateMcpToolName("ser-ver", strings.Repeat("x", 100), nil) == mcp.CreateMcpToolName("ser_ver", strings.Repeat("x", 100), nil) {
		t.Fatal("the hash does not cover the unsanitized names")
	}
}

func TestCreateMcpToolNameAddsTheHashSuffixWhenTheSanitizedNameIsTaken(t *testing.T) {
	taken := mcp.CreateMcpToolName("s", "a_b", nil)
	if taken != "mcp__s__a_b" {
		t.Fatalf("name = %q", taken)
	}
	isTaken := func(name string) bool { return name == taken }
	second := mcp.CreateMcpToolName("s", "a-b", isTaken)
	if !regexp.MustCompile(`^mcp__s__a_b_[0-9a-f]{8}$`).MatchString(second) {
		t.Fatalf("name = %q", second)
	}
	if mcp.CreateMcpToolName("s", "a_b", isTaken) == second {
		t.Fatal("the same tool and another tool got the same name")
	}
}

func TestMcpNamespaceIsMcpServerWithDashReplacedByUnderscore(t *testing.T) {
	if mcp.McpNamespace("docs") != "mcp__docs" || mcp.McpNamespace("my-server") != "mcp__my_server" || mcp.McpNamespace("my-server") != mcp.McpNamespace("my_server") {
		t.Fatal("namespace mismatch")
	}
	if !strings.HasPrefix(mcp.CreateMcpToolName("my-server", "search", nil), mcp.McpNamespace("my-server")+"__") {
		t.Fatal("the namespace is not the prefix of the tool name")
	}
}
