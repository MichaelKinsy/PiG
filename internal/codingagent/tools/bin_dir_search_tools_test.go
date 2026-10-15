package tools

// pi: packages/coding-agent/src/utils/tools-manager.ts

import (
	"path/filepath"
	"testing"
)

// grep.ts and find.ts read the same getBinDir() for their managed rg/fd lookup: Go's GrepToolOptions/FindToolOptions.BinDir, filled from
// ToolsOptions.BinDir, selects the ToolsManager rooted at the agent directory; with neither, only PATH is searched (no manager).
func TestSearchToolBinDirSelectsTheManagedToolsDirectory(t *testing.T) {
	managerOf := func(tool any) *ToolsManager {
		switch x := tool.(type) {
		case *GrepTool:
			return x.Tools
		case *FindTool:
			return x.Tools
		}
		t.Fatalf("unexpected tool %T", tool)
		return nil
	}
	agent, own := filepath.FromSlash("/agent"), filepath.FromSlash("/own")
	for _, name := range []string{"grep", "find"} {
		shared, err := CreateTool(name, "/", &ToolsOptions{BinDir: filepath.Join(agent, "bin")})
		if err != nil {
			t.Fatal(err)
		}
		if m := managerOf(shared); m == nil || m.agentDir != agent {
			t.Fatalf("%s from ToolsOptions.BinDir: manager %+v, want one rooted at %s", name, m, agent)
		}
		bare, err := CreateTool(name, "/", &ToolsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if m := managerOf(bare); m != nil {
			t.Fatalf("%s without BinDir: manager %+v, want none", name, m)
		}
	}
	if m := CreateGrepTool("/", &GrepToolOptions{BinDir: filepath.Join(own, "bin")}).Tools; m == nil || m.agentDir != own {
		t.Fatalf("grep own BinDir: manager %+v", m)
	}
}
