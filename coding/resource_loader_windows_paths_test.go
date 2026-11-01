package coding

import (
	"path/filepath"
	"strings"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Pi 1be312b4 (#10488) resource-loader.ts: findSourceInfoForPath matches a resource to the source that contains it with isPathWithin, and mergePaths deduplicates by
// getResourcePathKey; on Windows both ignore case, and a synthetic identifier keeps its own.
func TestResourceLoaderPathMatchingIgnoresCaseOnWindows(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pkg")
	resource := filepath.Join(strings.ToLower(root), "skills", "s", "SKILL.md")
	metadata := icodingagent.PathMetadata{Source: "npm:x", Scope: "user", Origin: "package", BaseDir: root}

	index := &pathMetadataIndex{}
	index.add(root, metadata)
	infos := extensionSourceInfos{paths: []string{root}, infos: map[string]icodingagent.PiSourceInfo{root: {Source: "npm:x", Scope: "user", Origin: "package"}}}
	if _, found := index.sourceInfo(resource); found {
		t.Fatal("metadata matched a path in other casing on a case-sensitive platform")
	}
	if _, found := infos.find(resource); found {
		t.Fatal("an extension's source info matched a path in other casing on a case-sensitive platform")
	}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	merged, err := loader.mergePaths([]string{root}, []string{strings.ToLower(root), "builtin:MCP", "builtin:mcp"})
	if err != nil || len(merged) != 4 {
		t.Fatalf("merged = %v, %v; want four paths on a case-sensitive platform", merged, err)
	}

	t.Cleanup(resolvepath.OverridePlatform("windows"))
	if info, found := index.sourceInfo(resource); !found || info.Source != "npm:x" {
		t.Fatalf("metadata did not match a path in other casing on Windows: %+v, %v", info, found)
	}
	if info, found := infos.find(resource); !found || info.Path != resource {
		t.Fatalf("an extension's source info did not match a path in other casing on Windows: %+v, %v", info, found)
	}
	merged, err = loader.mergePaths([]string{root}, []string{strings.ToLower(root), "builtin:MCP", "builtin:mcp"})
	if err != nil || len(merged) != 3 || merged[1] != "builtin:MCP" || merged[2] != "builtin:mcp" {
		t.Fatalf("merged = %v, %v; want the lower-case duplicate dropped and both builtin identifiers kept", merged, err)
	}
}
