package cli

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

// exitProcess ends the extension processes of the live Session hosts, after the startup host's orderly shutdown: os.Exit would leave the
// replacement Sessions' extension processes (D70) running for as long as they take to end, and Pi's extensions end with its process.
func TestExitProcessEndsTheSessionExtensionProcessesAfterTheStartupHost(t *testing.T) {
	const helper = "PIG_TEST_SESSION_EXTENSIONS_EXIT_HELPER"
	if os.Getenv(helper) != "" {
		stopStartupExtensions = func() { _, _ = os.Stdout.WriteString("startup host shut down\n") }
		terminateSessionExtensions = func() { _, _ = os.Stdout.WriteString("session extension processes ended\n") }
		exitProcess(7)
		return
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestExitProcessEndsTheSessionExtensionProcessesAfterTheStartupHost$")
	command.Env = append(os.Environ(), helper+"=1")
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit=%v output=%s", err, output)
	}
	if string(output) != "startup host shut down\nsession extension processes ended\n" {
		t.Fatalf("exit cleanup output=%q", output)
	}
}
