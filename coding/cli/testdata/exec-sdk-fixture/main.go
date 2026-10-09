// Command exec-sdk-fixture is a minimal Go-SDK extension that exercises
// pi.exec (sdk.Context.Exec) through the real host wire format, so the
// exec-all-modes regression test covers the Go SDK path, not only Node.
package main

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func main() {
	ext := sdk.New("exec-sdk-fixture")
	ext.Command("exec_probe", "call pi.exec and report the result", func(ctx sdk.Context, _ string) error {
		// PIG_TEST_EXEC_HELPER_PATH points at a portable, natively executable
		// helper (the test binary itself, run in "echo" mode) instead of an
		// external echo binary, so this fixture also works on native Windows.
		helper := os.Getenv("PIG_TEST_EXEC_HELPER_PATH")
		if helper == "" {
			return fmt.Errorf("PIG_TEST_EXEC_HELPER_PATH not set")
		}
		result, err := ctx.Exec(helper, []string{"exec-probe-ok"})
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("exec code = %d, stderr = %q", result.ExitCode, result.Stderr)
		}
		if result.Stdout != "exec-probe-ok\n" {
			return fmt.Errorf("exec stdout = %q", result.Stdout)
		}
		return nil
	})
	if err := ext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "exec-sdk-fixture: %v\n", err)
		os.Exit(1)
	}
}
