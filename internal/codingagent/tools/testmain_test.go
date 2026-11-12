package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain lets the test binary stand in for the shell and report how it was
// started or what environment it received. It scopes the temporary directory to
// this run: the bash tool keeps a pi-bash-*.log for output it truncates.
func TestMain(m *testing.M) {
	if fakeToolVersion() {
		return
	}
	reportShellEnvIfRequested()
	testenv.GrabTerminalIfRequested()
	testenv.ReportStartupIfRequested()
	testenv.ReportEnvironIfRequested()
	os.Exit(testenv.RunScoped(m, "pig-tools-"))
}

// fakeToolVersion makes a copy of the test binary named fd, fdfind or rg
// (executableScript on Windows) answer "--version" as that tool does, so it
// passes the identity probe. It reports whether it answered.
func fakeToolVersion() bool {
	if len(os.Args) != 2 || os.Args[1] != "--version" {
		return false
	}
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	switch name {
	case "fd", "fdfind":
		fmt.Println(name + " 10.3.0")
	case "rg":
		fmt.Println("ripgrep 14.1.0")
	default:
		return false
	}
	return true
}
