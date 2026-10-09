package tools

import "testing"

// Pi createBashTool/createPowerShellTool/createGrepTool/createFindTool(cwd, options) read the managed binary directory from
// getBinDir(); Go carries it in the tool's options, and ToolsOptions.BinDir fills it for the createTool/createXTools factories.
func TestToolBinDirComesFromOptions(t *testing.T) {
	bin := t.TempDir() + "/agent/bin"
	if got := CreateBashTool("/c", &BashToolOptions{BinDir: bin}).BinDir; got != bin {
		t.Errorf("bash BinDir = %q, want %q", got, bin)
	}
	if got := CreatePowerShellTool("/c", &PowerShellToolOptions{BinDir: bin}).BinDir; got != bin {
		t.Errorf("powershell BinDir = %q, want %q", got, bin)
	}
	if CreateGrepTool("/c", &GrepToolOptions{BinDir: bin}).Tools == nil || CreateFindTool("/c", &FindToolOptions{BinDir: bin}).Tools == nil {
		t.Error("grep and find build their tools manager from options.BinDir")
	}
	if CreateGrepTool("/c", nil).Tools != nil || CreateFindTool("/c", &FindToolOptions{}).Tools != nil {
		t.Error("without a BinDir grep and find search PATH only")
	}
	if CreateBashTool("/c", nil).BinDir != "" {
		t.Error("nil options carry no BinDir")
	}

	shared := &ToolsOptions{BinDir: bin}
	tool, err := CreateTool("bash", "/c", shared)
	if err != nil || tool.(*BashTool).BinDir != bin {
		t.Errorf("CreateTool bash: BinDir = %v, err %v, want %q", tool, err, bin)
	}
	for _, set := range [][]string{{"bash"}, {"powershell"}} {
		tool, _ := CreateTool(set[0], "/c", shared)
		if v, ok := tool.(*PowerShellTool); ok && v.BinDir != bin {
			t.Errorf("powershell via CreateTool: BinDir = %q", v.BinDir)
		}
	}
	for _, name := range []string{"grep", "find"} {
		tool, _ := CreateTool(name, "/c", shared)
		switch v := tool.(type) {
		case *GrepTool:
			if v.Tools == nil {
				t.Error("grep via CreateTool has no tools manager")
			}
		case *FindTool:
			if v.Tools == nil {
				t.Error("find via CreateTool has no tools manager")
			}
		}
	}
	for _, tl := range CreateAllTools("/c", shared) {
		if b, ok := tl.(*BashTool); ok && b.BinDir != bin {
			t.Errorf("CreateAllTools bash BinDir = %q", b.BinDir)
		}
		if p, ok := tl.(*PowerShellTool); ok && p.BinDir != bin {
			t.Errorf("CreateAllTools powershell BinDir = %q", p.BinDir)
		}
	}
	for _, tl := range CreateReadOnlyTools("/c", shared) {
		if g, ok := tl.(*GrepTool); ok && g.Tools == nil {
			t.Error("CreateReadOnlyTools grep has no tools manager")
		}
	}
}
