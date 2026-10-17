package tools

import "testing"

// TestCreateToolsTakeCwdAndOptions mirrors upstream createBashTool, createPowerShellTool, createGrepTool and createFindTool (bash.ts:434, powershell.ts:59, grep.ts:321, find.ts:316): each takes
// (cwd, options). Pi reads getBinDir() and its module-level file mutation queue globally; Go carries them in the options.
func TestCreateToolsTakeCwdAndOptions(t *testing.T) {
	if got := CreateBashTool("/c", &BashToolOptions{BinDir: "/bin"}).BinDir; got != "/bin" {
		t.Fatalf("bash BinDir = %q", got)
	}
	if got := CreatePowerShellTool("/c", &PowerShellToolOptions{BinDir: "/bin"}).BinDir; got != "/bin" {
		t.Fatalf("powershell BinDir = %q", got)
	}
	if CreateGrepTool("/c", &GrepToolOptions{BinDir: "/bin"}).Tools == nil || CreateFindTool("/c", &FindToolOptions{BinDir: "/bin"}).Tools == nil {
		t.Fatal("search tools lost their manager")
	}
}

// TestCreateCodingToolsInjectsBinDir checks the built-in set (createCodingTools, tools/index.ts:195): ToolsOptions.BinDir reaches bash
// and the caller's option structs are not modified.
func TestCreateCodingToolsInjectsBinDir(t *testing.T) {
	bash := &BashToolOptions{}
	set := CreateCodingTools("/c", &ToolsOptions{BinDir: "/managed", Bash: bash})
	var b *BashTool
	for _, tool := range set {
		if v, ok := tool.(*BashTool); ok {
			b = v
		}
	}
	if b == nil || b.BinDir != "/managed" {
		t.Fatalf("bash BinDir not injected: %+v", b)
	}
	if bash.BinDir != "" {
		t.Fatal("the caller's BashToolOptions was modified")
	}
}
