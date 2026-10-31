// Ports packages/coding-agent/src/config.ts (getConfiguredSelfUpdateArgv, makeConfiguredSelfUpdateCommand), Pi 8bc94b19.
package codingagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/crossspawn"
)

// ConfiguredSelfUpdateArgv is the self-update command the user configured: PI_SELF_UPDATE_COMMAND (whitespace-separated
// argv, no quoting), else the global selfUpdateCommand setting. It overrides installation-tier detection.
// pig divergence (D39): PIG_SELF_UPDATE_COMMAND, PiG's product-neutral alias, is read first, as PIG_PACKAGE_DIR precedes
// PI_PACKAGE_DIR; the user's explicit command is the one owner of the update, selected before any tier.
func ConfiguredSelfUpdateArgv(setting []string) []string {
	for _, name := range []string{"PIG_SELF_UPDATE_COMMAND", "PI_SELF_UPDATE_COMMAND"} {
		if argv := strings.FieldsFunc(jsTrim(os.Getenv(name)), isJSWhitespace); len(argv) > 0 {
			return argv
		}
	}
	if len(setting) > 0 {
		return setting
	}
	return nil
}

// SelfUpdateRelease is the release a configured self-update command installs.
type SelfUpdateRelease struct {
	PackageName string
	Version     string
}

// MakeConfiguredSelfUpdateCommand fills the {packageName}, {version} and {installSpec} placeholders of a configured
// self-update command from release. Any other brace text stays as written.
func MakeConfiguredSelfUpdateCommand(argv []string, release SelfUpdateRelease) *SelfUpdateCommand {
	replacer := strings.NewReplacer(
		"{packageName}", release.PackageName,
		"{version}", release.Version,
		"{installSpec}", release.PackageName+"@"+release.Version,
	)
	filled := make([]string, len(argv))
	for i, arg := range argv {
		filled[i] = replacer.Replace(arg)
	}
	var command string
	var args []string
	if len(filled) > 0 {
		command, args = filled[0], filled[1:]
	}
	return makeSelfUpdateCommandStep(command, args)
}

// RunConfiguredSelfUpdateCommand runs the user's own update command with the terminal attached. It has no deadline: the
// command is the user's, as Pi's runSelfUpdate spawns it with inherited stdio and waits.
func RunConfiguredSelfUpdateCommand(cmd *SelfUpdateCommand) error {
	c := crossspawn.Command(context.Background(), "", cmd.Command, cmd.Args...)
	c.Stdout, c.Stderr, c.Stdin = os.Stdout, os.Stderr, os.Stdin
	err := c.Run()
	if err == nil {
		return nil
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		if code := exit.ExitCode(); code >= 0 {
			return fmt.Errorf("%s exited with code %d", cmd.Display, code)
		}
		return fmt.Errorf("%s terminated by a signal", cmd.Display)
	}
	return err
}
