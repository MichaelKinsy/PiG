//go:build windows

package codingagent

import (
	"context"
	"os/exec"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/crossspawn"
)

// editorCommand runs the editor through the command shell, as upstream spawns
// it with shell: true on Windows: Node joins the command and its arguments
// with spaces and runs %ComSpec% /d /s /c "<line>" verbatim, so .cmd editors
// and %VAR% references work as they do at a prompt. A non-cmd ComSpec instead
// receives one -c command argument, as Node does.
func editorCommand(ctx context.Context, name string, args []string) *exec.Cmd {
	line := strings.Join(append([]string{name}, args...), " ")
	return crossspawn.ShellCommand(ctx, line)
}
