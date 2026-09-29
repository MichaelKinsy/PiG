package crossspawn

import (
	"slices"
	"testing"
)

// Pi external-editor.ts's shell:true and resolve-config-value.ts's execSync
// use Node child_process.normalizeSpawnArguments. Only a cmd-named ComSpec
// receives /d /s /c and verbatim arguments; other configured shells use -c.
func TestWindowsShellPlanMatchesNode(t *testing.T) {
	line := `editor "file & name" %VALUE% | next`
	for _, shell := range []string{"cmd.exe", "CMD", `C:\Windows\System32\cmd.exe`} {
		plan := planWindowsShell(shell, line)
		if plan.name != shell || plan.cmdLine != shell+` /d /s /c "`+line+`"` {
			t.Errorf("cmd shell %q: %#v", shell, plan)
		}
	}
	for _, shell := range []string{"sh.exe", `C:\shell tools\bash.exe`, "mycmd.exe", "C:/Windows/System32/cmd.exe"} {
		plan := planWindowsShell(shell, line)
		if plan.name != shell || plan.cmdLine != "" || !slices.Equal(plan.args, []string{"-c", line}) {
			t.Errorf("non-cmd shell %q: %#v; want direct -c with one command argument", shell, plan)
		}
	}
}
