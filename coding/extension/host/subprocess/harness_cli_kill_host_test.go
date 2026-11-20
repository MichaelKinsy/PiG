//go:build !windows && !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// A Node extension runs the harness entry of the materialized runtime (packed or isolated), not the one in the source tree: it starts the entry again the way pi-subagents does (process.execPath process.argv[1] ...), and SIGKILL of that entry must end the binary the entry runs.
func TestHarnessEntryKillAcrossIsolationModes(t *testing.T) {
	for _, isolation := range []string{"shared-ok", "strict"} {
		t.Run(isolation, func(t *testing.T) {
			privatePackedLogTemp(t)
			dir := t.TempDir()
			binaryPID, entryPID, argsFile := filepath.Join(dir, "bin.pid"), filepath.Join(dir, "entry.pid"), filepath.Join(dir, "args")
			stub := filepath.Join(dir, "pig")
			termFile := filepath.Join(dir, "term")
			script := "#!/bin/sh\ntrap 'echo > " + termFile + "' TERM\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\necho $$ > '" + binaryPID + ".tmp' && mv '" + binaryPID + ".tmp' '" + binaryPID + "'\nwhile :; do sleep 0.1; done\n"
			if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			ext := filepath.Join(dir, "respawn.mjs")
			source := `import { spawn } from "node:child_process";
import { writeFileSync } from "node:fs";
export default function (pi) {
  const child = spawn(process.execPath, [process.argv[1], "--extension-arg"], { stdio: "ignore" });
  writeFileSync(` + strconv.Quote(entryPID) + `, String(child.pid));
}
`
			if err := os.WriteFile(ext, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			h.harnessBinary = stub
			h.harnessArgs = []string{"--mode", "rpc"}
			t.Cleanup(func() { h.Shutdown("test done") })
			if _, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "respawn", Source: ext, Isolation: isolation, Enabled: true}}); len(errs) != 0 {
				t.Fatal(errs)
			}
			entry, binary := waitHarnessChildPID(t, entryPID), waitHarnessChildPID(t, binaryPID)
			t.Cleanup(func() {
				_ = syscall.Kill(binary, syscall.SIGKILL)
				_ = syscall.Kill(entry, syscall.SIGKILL)
			})
			if raw, err := os.ReadFile(argsFile); err != nil || strings.TrimSpace(string(raw)) != "--extension-arg" {
				t.Fatalf("binary argv = %q (%v), want exactly the arguments the extension passed", raw, err)
			}
			supervisor := harnessSupervisor(t, binary, entry)
			if err := syscall.Kill(entry, syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			awaitHarnessFile(t, termFile)
			if !harnessChildAlive(binary) {
				t.Fatal("the binary ended on TERM, which it traps")
			}
			if err := syscall.Kill(entry, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			awaitHarnessBinaryEnd(t, binary, supervisor)
		})
	}
}
