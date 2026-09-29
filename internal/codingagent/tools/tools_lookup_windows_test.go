//go:build windows

package tools

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's getToolPath (utils/tools-manager.ts) returns a system tool's name when
// commandExists can spawnSync it. Node's spawn finds only .com and .exe
// programs, from the working directory first unless
// NoDefaultCurrentDirectoryInExePath is set, so fd.cmd is no fd. Pi's own
// getToolPath, with an empty agent bin directory, is the oracle.
func TestLookupToolPathFindsToolsAsPiDoes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	module, err := filepath.Abs("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/utils/tools-manager.js")
	if err != nil {
		t.Fatal(err)
	}
	if module, err = filepath.EvalSymlinks(module); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	bin := filepath.Join(root, "bin")
	agentDir := filepath.Join(root, "agent")
	for _, dir := range []string{cwd, bin, agentDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string, content []byte) {
		t.Helper()
		if err := os.WriteFile(path, content, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(bin, "fd.cmd"), []byte("@echo fd\r\n"))
	write(filepath.Join(bin, "fdfind.bat"), []byte("@echo fdfind\r\n"))
	write(filepath.Join(bin, "rg.cmd"), []byte("@echo rg\r\n"))
	write(filepath.Join(cwd, "rg.exe"), executableScript(t, "rg"))
	t.Chdir(cwd)
	t.Setenv("PATH", bin)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	tools := []string{"fd", "rg"}
	compare := func(t *testing.T) {
		t.Helper()
		input, err := json.Marshal(map[string]any{"module": module, "tools": tools})
		if err != nil {
			t.Fatal(err)
		}
		pi := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, tools } = JSON.parse(readFileSync(0, "utf8"));
const { getToolPath } = await import(pathToFileURL(module).href);
process.stdout.write(JSON.stringify(tools.map((tool) => getToolPath(tool) ?? "")));
`)
		pi.Stdin = bytes.NewReader(input)
		out, err := pi.Output()
		if err != nil {
			t.Fatalf("Pi getToolPath: %v; output %s", err, out)
		}
		var want []string
		if err := json.Unmarshal(out, &want); err != nil || len(want) != len(tools) {
			t.Fatalf("decode Pi paths %s: %v", out, err)
		}
		for i, tool := range tools {
			if got := LookupToolPath(tool, filepath.Join(agentDir, "bin")); got != want[i] {
				t.Errorf("LookupToolPath(%q) = %q, want Pi's %q", tool, got, want[i])
			}
		}
	}
	for _, searchCwd := range []bool{true, false} {
		t.Run(map[bool]string{true: "cwd searched", false: "NoDefaultCurrentDirectoryInExePath"}[searchCwd], func(t *testing.T) {
			t.Setenv("NoDefaultCurrentDirectoryInExePath", "1")
			if searchCwd {
				if err := os.Unsetenv("NoDefaultCurrentDirectoryInExePath"); err != nil {
					t.Fatal(err)
				}
			}
			compare(t)
			fdfind := filepath.Join(bin, "fdfind.exe")
			write(fdfind, executableScript(t, "fdfind"))
			compare(t)
			if err := os.Remove(fdfind); err != nil {
				t.Fatal(err)
			}
		})
	}
}
