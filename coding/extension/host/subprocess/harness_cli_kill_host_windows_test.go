//go:build windows && !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestHarnessEntryKillAcrossIsolationModesWindows exercises the materialized
// Node runtime's harness entry (both packed/shared-ok and isolated/strict) on
// Windows:
//   - "terminate-entry": the extension spawns process.execPath process.argv[1]
//     (--extension-arg); terminating the spawned entry via TerminateProcess must
//     end the binary and its supervisor within testbudget.Wait(t).
//   - "host-shutdown": stopping the Host via h.Shutdown (process_tree_windows.go
//     TerminateJobObject) must end the extension, the entry, its supervisor, and
//     the binary together.
func TestHarnessEntryKillAcrossIsolationModesWindows(t *testing.T) {
	binary := harnessSelfBinaryPath(t)
	for _, isolation := range []string{"shared-ok", "strict"} {
		for _, mode := range []string{"terminate-entry", "host-shutdown"} {
			t.Run(isolation+"/"+mode, func(t *testing.T) {
				privatePackedLogTemp(t)
				dir := t.TempDir()
				entryPIDFile := filepath.Join(dir, "entry.pid")
				binaryPIDFile := filepath.Join(dir, "binary.pid")
				argsFile := filepath.Join(dir, "binary.args.json")

				t.Setenv(harnessTestHelperEnv, "wait")
				t.Setenv(harnessTestHelperPIDEnv, binaryPIDFile)
				t.Setenv(harnessTestHelperArgsEnv, argsFile)

				ext := filepath.Join(dir, "respawn.mjs")
				if err := os.WriteFile(ext, []byte(harnessRespawnExtensionSource(entryPIDFile, "--extension-arg")), 0o600); err != nil {
					t.Fatal(err)
				}

				h := NewHost(t.TempDir())
				h.harnessBinary = binary
				h.harnessArgs = []string{"--mode", "rpc"}
				t.Cleanup(func() { h.Shutdown("test done") })
				if _, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "respawn", Source: ext, Isolation: isolation, Enabled: true}}); len(errs) != 0 {
					t.Fatal(errs)
				}

				entryPID := waitHarnessChildPID(t, entryPIDFile)
				binaryPID := waitHarnessChildPID(t, binaryPIDFile)
				entryHandle := openWindowsProcessHandle(t, entryPID, "entry")
				binaryHandle := openWindowsProcessHandle(t, binaryPID, "binary")
				supervisorPID := harnessSupervisorWindows(t, binaryPID, entryPID)
				supervisorHandle := openWindowsProcessHandle(t, supervisorPID, "supervisor")

				rawArgs, err := os.ReadFile(argsFile)
				if err != nil {
					t.Fatalf("read args file %s: %v", argsFile, err)
				}
				var gotArgs []string
				if err := json.Unmarshal(rawArgs, &gotArgs); err != nil {
					t.Fatalf("unmarshal args %q: %v", rawArgs, err)
				}
				if !slices.Equal(gotArgs, []string{"--extension-arg"}) {
					t.Fatalf("binary argv = %q, want [--extension-arg]", gotArgs)
				}

				switch mode {
				case "terminate-entry":
					if err := windows.TerminateProcess(entryHandle, 1); err != nil {
						t.Fatalf("TerminateProcess(entry pid=%d): %v", entryPID, err)
					}
					_, entryElapsed := awaitWindowsHandleExit(t, entryHandle, entryPID, "entry", supervisorPID, binaryPID)
					binCode, binElapsed := awaitWindowsHandleExit(t, binaryHandle, binaryPID, "binary", entryPID, supervisorPID)
					supCode, supElapsed := awaitWindowsHandleExit(t, supervisorHandle, supervisorPID, "supervisor", entryPID, binaryPID)
					t.Logf("%s/%s: entry(pid=%d) gone in %s; binary(pid=%d, exit=%d) gone in %s; supervisor(pid=%d, exit=%d) gone in %s",
						isolation, mode,
						entryPID, entryElapsed.Round(time.Millisecond),
						binaryPID, binCode, binElapsed.Round(time.Millisecond),
						supervisorPID, supCode, supElapsed.Round(time.Millisecond))
				case "host-shutdown":
					h.Shutdown("test host shutdown")
					_, entryElapsed := awaitWindowsHandleExit(t, entryHandle, entryPID, "entry", supervisorPID, binaryPID)
					binCode, binElapsed := awaitWindowsHandleExit(t, binaryHandle, binaryPID, "binary", entryPID, supervisorPID)
					supCode, supElapsed := awaitWindowsHandleExit(t, supervisorHandle, supervisorPID, "supervisor", entryPID, binaryPID)
					t.Logf("%s/%s: after Host.Shutdown entry(pid=%d) gone in %s; binary(pid=%d, exit=%d) gone in %s; supervisor(pid=%d, exit=%d) gone in %s",
						isolation, mode,
						entryPID, entryElapsed.Round(time.Millisecond),
						binaryPID, binCode, binElapsed.Round(time.Millisecond),
						supervisorPID, supCode, supElapsed.Round(time.Millisecond))
				}
				awaitWindowsNoChildren(t, fmt.Sprintf("%s/%s entry pid=%d supervisor pid=%d", isolation, mode, entryPID, supervisorPID), entryPID, supervisorPID)
			})
		}
	}
}
