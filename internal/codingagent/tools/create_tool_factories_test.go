package tools

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream createReadTool, createEditTool, createWriteTool, createGrepTool, createFindTool and createLsTool each build their tool from (cwd, options): the working
// directory and every option reach the tool, an absent option leaves the tool's defaults, and every edit and write tool shares the process mutation queue.
// Pi: packages/coding-agent/src/core/tools/read.ts createReadTool; edit.ts:218 createEditTool; write.ts:95 createWriteTool; grep.ts:321 createGrepTool; find.ts:316 createFindTool; ls.ts:173 createLsTool.
func TestCreateToolFactoriesBindTheirCwdAndOptions(t *testing.T) {
	resize := false
	resizeOptions := &ai.ModelImageResizeOptions{MaxWidth: 7}
	readOps, editOps, writeOps := &ReadOperations{}, &EditOperations{}, &WriteOperations{}
	grepOps, findOps, lsOps := &GrepOperations{}, &FindOperations{}, &LsOperations{}

	read := CreateReadTool("/r", &ReadToolOptions{AutoResizeImages: &resize, ResizeOptions: resizeOptions, Operations: readOps})
	if read.CWD != "/r" || read.AutoResizeImages != &resize || read.ResizeOptions != resizeOptions || read.Operations != readOps {
		t.Errorf("CreateReadTool = %+v", read)
	}
	if bare := CreateReadTool("/r2", nil); bare.CWD != "/r2" || bare.AutoResizeImages != nil || bare.ResizeOptions != nil || bare.Operations != nil {
		t.Errorf("CreateReadTool without options = %+v", bare)
	}

	edit := CreateEditTool("/e", &EditToolOptions{Operations: editOps})
	write := CreateWriteTool("/w", &WriteToolOptions{Operations: writeOps})
	if edit.CWD != "/e" || edit.Operations != editOps || write.CWD != "/w" || write.Operations != writeOps {
		t.Errorf("edit = %+v, write = %+v", edit, write)
	}
	if edit.Queue == nil || edit.Queue != write.Queue || edit.Queue != CreateEditTool("/e2", nil).Queue {
		t.Error("edit and write tools do not share one process mutation queue")
	}
	if bare := CreateEditTool("/e", nil); bare.Operations != nil {
		t.Errorf("CreateEditTool without options = %+v", bare)
	}

	grep := CreateGrepTool("/g", &GrepToolOptions{Operations: grepOps, BinDir: "/data/bin"})
	find := CreateFindTool("/f", &FindToolOptions{Operations: findOps, BinDir: "/data/bin"})
	if grep.CWD != "/g" || grep.Operations != grepOps || grep.Tools == nil || find.CWD != "/f" || find.Operations != findOps || find.Tools == nil {
		t.Errorf("grep = %+v, find = %+v", grep, find)
	}
	if bare := CreateGrepTool("/g", nil); bare.Tools != nil || bare.Operations != nil {
		t.Errorf("CreateGrepTool without options = %+v", bare)
	}
	if bare := CreateFindTool("/f", nil); bare.Tools != nil || bare.Operations != nil {
		t.Errorf("CreateFindTool without options = %+v", bare)
	}

	ls := CreateLsTool("/l", &LsToolOptions{Operations: lsOps})
	if ls.CWD != "/l" || ls.Operations != lsOps {
		t.Errorf("CreateLsTool = %+v", ls)
	}
	if bare := CreateLsTool("/l", nil); bare.Operations != nil {
		t.Errorf("CreateLsTool without options = %+v", bare)
	}
}

// upstream createReadOnlyTools(cwd, options) passes each tool its own options, and the grep and find tools receive the managed tools directory.
// Pi: packages/coding-agent/src/core/tools/index.ts:195 createReadOnlyTools.
func TestCreateReadOnlyToolsPassesEachToolItsOptionsAndTheBinDir(t *testing.T) {
	readOps, lsOps := &ReadOperations{}, &LsOperations{}
	grepOps, findOps := &GrepOperations{}, &FindOperations{}
	set := CreateReadOnlyTools("/ro", &ToolsOptions{
		BinDir: "/data/bin",
		Read:   &ReadToolOptions{Operations: readOps}, Grep: &GrepToolOptions{Operations: grepOps},
		Find: &FindToolOptions{Operations: findOps}, Ls: &LsToolOptions{Operations: lsOps},
	})
	read, grep, find, ls := set[0].(*ReadTool), set[1].(*GrepTool), set[2].(*FindTool), set[3].(*LsTool)
	if read.CWD != "/ro" || read.Operations != readOps || grep.CWD != "/ro" || grep.Operations != grepOps || find.CWD != "/ro" || find.Operations != findOps || ls.CWD != "/ro" || ls.Operations != lsOps {
		t.Errorf("options or cwd lost: read=%+v grep=%+v find=%+v ls=%+v", read, grep, find, ls)
	}
	if grep.Tools == nil || find.Tools == nil {
		t.Error("grep and find did not receive the managed tools directory")
	}
	if bare := CreateReadOnlyTools("/ro", nil); bare[1].(*GrepTool).Tools != nil || bare[2].(*FindTool).Tools != nil {
		t.Error("without a BinDir grep and find search PATH only")
	}
}

// upstream createBashTool and createPowerShellTool run commands in the cwd they were created with; createBashTool resolves its shell from the settings it was given.
// Pi: packages/coding-agent/src/core/tools/bash.ts:434 createBashTool; powershell.ts:59 createPowerShellTool.
func TestCreateShellToolsBindTheirCwdAndSettings(t *testing.T) {
	settings := fixedShellPath("/bin/sh")
	bash := CreateBashTool("/b", &BashToolOptions{Settings: settings})
	if bash.CWD != "/b" || bash.Settings != SettingsView(settings) {
		t.Errorf("CreateBashTool = %+v", bash)
	}
	if shell, err := CreateBashTool("/b", &BashToolOptions{Settings: settings, ShellPath: "/bin/bash"}).Settings.GetShellPath(); err != nil || shell != "/bin/bash" {
		t.Errorf("an explicit ShellPath must win over the settings: %q, %v", shell, err)
	}
	powershell := CreatePowerShellTool("/p", nil)
	if powershell.CWD != "/p" || powershell.HideSessionEnvironment || powershell.SpawnHook != nil {
		t.Errorf("CreatePowerShellTool = %+v", powershell)
	}
	hooked := CreatePowerShellTool("/p", &PowerShellToolOptions{ExposeSessionEnvironment: new(false), SpawnHook: func(c BashSpawnContext) BashSpawnContext { return c }})
	if !hooked.HideSessionEnvironment || hooked.SpawnHook == nil {
		t.Errorf("CreatePowerShellTool with options = %+v", hooked)
	}
}
