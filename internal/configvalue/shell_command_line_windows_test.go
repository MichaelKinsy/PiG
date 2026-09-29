//go:build windows

package configvalue

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi resolves a !command with spawnSync(shell, ["-c", command]) through
// getShellConfig's Git Bash (core/resolve-config-value.ts
// executeWithConfiguredShell), so bash receives libuv's command line and
// parses it with MSYS2 rules. A BASH_ENV script prints BASH_EXECUTION_STRING,
// the -c argument as bash received it, into the resolved value. The commands
// succeed under Pi and hold double quotes with and without spaces, backslash
// runs, empty strings, and a tab.
func TestResolveBangCmdPassesCommandToGitBashAsPiDoes(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "execution-string.sh")
	if err := os.WriteFile(probe, []byte("printf '<%s>\\n' \"$BASH_EXECUTION_STRING\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", probe)
	configs := []string{
		`!true""`,
		`!""true`,
		`!echo${IFS}"a\"b"`,
		`!echo${IFS}a\\"b"`,
		`!echo "x y"`,
		`!echo a\"b trailing\`,
		`!printf '[%s]' "" ''`,
		"!printf '%s|' \"tab\there\"",
	}
	root, err := filepath.EvalSymlinks("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "core", "resolve-config-value.js"), "configs": configs})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, configs } = JSON.parse(readFileSync(0, "utf8"));
const { resolveConfigValueUncached } = await import(pathToFileURL(module).href);
process.stdout.write(JSON.stringify(configs.map((config) => resolveConfigValueUncached(config) ?? "")));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi resolveConfigValueUncached: %v; output %s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(configs) {
		t.Fatalf("decode Pi values %s: %v", out, err)
	}
	for i, config := range configs {
		if got := ResolveUncached(config, nil); got != want[i] {
			t.Errorf("config %q\n got %q\nwant %q", config, got, want[i])
		}
	}
}
