package crossspawn

import (
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

var cmdShell = lazyregexp.New(`(?i)^(?:.*\\)?cmd(?:\.exe)?$`)

// planWindowsShell mirrors Node's shell:true command transport. The input is
// intentionally shell source, unlike the literal arguments to Command.
func planWindowsShell(shell, line string) windowsCommandPlan {
	if !cmdShell.MatchString(shell) {
		return windowsCommandPlan{name: shell, args: []string{"-c", line}}
	}
	return windowsCommandPlan{name: shell, cmdLine: shell + ` /d /s /c "` + line + `"`}
}
