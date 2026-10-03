package mcpext_test

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// Ports packages/coding-agent/test/mcp-extension.test.ts ("MCP servers section", 0.99.2).

func sectionServer(name, description string, exposure extension.McpExposure) mcpext.McpServerListing {
	return mcpext.McpServerListing{Entry: mcpext.McpServerEntry{
		Name: name, Source: "test",
		Config: extension.McpServerConfig{Command: "x", Description: description, Exposure: exposure},
	}}
}

// utf16Len is JavaScript's string length.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

func TestMCPServersSectionListsServersWithHowTheirToolsAreReachedAndTheFirstLineOfTheirDescription(t *testing.T) {
	plain := sectionServer("plain", "", "")
	plain.Instructions = "From instructions."
	section, ok := mcpext.RenderServersSection([]mcpext.McpServerListing{
		sectionServer("docs", "Docs search.\nMore.", ""),
		sectionServer("later", "", extension.McpExposureDeferred),
		sectionServer("direct", "Declared.", extension.McpExposureDirect),
		plain,
	})
	if !ok {
		t.Fatal("no section")
	}
	want := []string{
		"- mcp__docs (codemode): Docs search.",
		"- mcp__later (tool_search)",
		"- mcp__plain (codemode): From instructions.",
	}
	if got := strings.Split(section, "\n")[1:]; !slices.Equal(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
	if _, ok := mcpext.RenderServersSection([]mcpext.McpServerListing{sectionServer("direct", "Declared.", extension.McpExposureDirect)}); ok {
		t.Fatal("a server with only direct tools got a section")
	}
}

func TestMCPServersSectionShortensDescriptionsToFitTheSizeLimit(t *testing.T) {
	var servers []mcpext.McpServerListing
	for i := range 40 {
		servers = append(servers, sectionServer(fmt.Sprintf("server%d", i), strings.Repeat("x", 400), ""))
	}
	section, _ := mcpext.RenderServersSection(servers)
	if n := utf16Len(section); n > mcpext.MaxServersSectionChars {
		t.Fatalf("section has %d chars, limit %d", n, mcpext.MaxServersSectionChars)
	}
	if n := len(strings.Split(section, "\n")); n != 41 {
		t.Fatalf("section has %d lines, want 41", n)
	}
	if !strings.Contains(section, "- mcp__server39 (codemode): x") {
		t.Fatalf("section = %q", section)
	}
}

func TestMCPServersSectionLeavesOutTheLastServersWhenTheirNamesAloneDoNotFit(t *testing.T) {
	var servers []mcpext.McpServerListing
	for i := range 200 {
		servers = append(servers, sectionServer(fmt.Sprintf("server-with-a-long-name-%d", i), "desc", ""))
	}
	section, _ := mcpext.RenderServersSection(servers)
	if n := utf16Len(section); n > mcpext.MaxServersSectionChars {
		t.Fatalf("section has %d chars, limit %d", n, mcpext.MaxServersSectionChars)
	}
	lines := strings.Split(section, "\n")
	last := lines[len(lines)-1]
	match := regexp.MustCompile(`^- … (\d+) more servers; find their tools with searchTools\(\)$`).FindStringSubmatch(last)
	if match == nil {
		t.Fatalf("last line = %q", last)
	}
	omitted, _ := strconv.Atoi(match[1])
	if got := len(lines) - 2 + omitted; got != 200 {
		t.Fatalf("%d listed + %d omitted = %d, want 200", len(lines)-2, omitted, got)
	}
}

// upstream: packages/coding-agent/src/extensions/mcp/index.ts serversSectionIntro (1.0.0): the first line explains only
// the ways of reaching tools that the listed servers use.
func TestMCPServersSectionIntroExplainsOnlyTheReachesTheListedServersUse(t *testing.T) {
	const base = "MCP servers whose tools are not declared to you."
	const codemode = " Call the tools of `codemode` servers from codemode scripts."
	const toolSearch = " Load the tools of `tool_search` servers with `tool_search`."
	for _, c := range []struct {
		name    string
		servers []mcpext.McpServerListing
		want    string
	}{
		{"codemode", []mcpext.McpServerListing{sectionServer("docs", "", "")}, base + codemode},
		{"tool_search", []mcpext.McpServerListing{sectionServer("later", "", extension.McpExposureDeferred)}, base + toolSearch},
		{"both", []mcpext.McpServerListing{sectionServer("later", "", extension.McpExposureDeferred), sectionServer("docs", "", "")}, base + codemode + toolSearch},
	} {
		section, ok := mcpext.RenderServersSection(c.servers)
		if !ok {
			t.Fatalf("%s: no section", c.name)
		}
		if intro, _, _ := strings.Cut(section, "\n"); intro != c.want {
			t.Errorf("%s: intro = %q, want %q", c.name, intro, c.want)
		}
	}
}
