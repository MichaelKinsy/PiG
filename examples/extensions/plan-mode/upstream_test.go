package planmode

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

type utilityProbe struct {
	Method string `json:"method"`
	Input  string `json:"input"`
}

func probeUpstreamUtilities(t *testing.T, probes []utilityProbe) []json.RawMessage {
	t.Helper()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/upstream-utils.mjs", "../../../.upstream/current/packages/coding-agent/examples/extensions/plan-mode/utils.ts")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("pinned upstream utility probe: %v\n%s", err, &stderr)
	}
	var results []json.RawMessage
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(probes) {
		t.Fatalf("upstream returned %d results for %d inputs", len(results), len(probes))
	}
	return results
}

func TestCommandPatternCorpusAgainstUpstream(t *testing.T) {
	// Representatives cover every SAFE_PATTERNS/DESTRUCTIVE_PATTERNS entry in utils.ts.
	safe := []string{
		"cat", "head", "tail", "less", "more", "grep", "find", "ls", "pwd", "echo", "printf", "wc", "sort", "uniq", "diff", "file", "stat", "du", "df", "tree", "which", "whereis", "type", "env", "printenv", "uname", "whoami", "id", "date", "cal", "uptime", "ps", "top", "htop", "free",
		"git status", "git log", "git diff", "git show", "git branch", "git remote", "git config --get", "git ls-files", "npm list", "npm ls", "npm view", "npm info", "npm search", "npm outdated", "npm audit", "yarn list", "yarn info", "yarn why", "yarn audit", "node --version", "python --version", "curl ", "wget -O-", "jq", "sed -n", "awk", "rg", "fd", "bat", "eza",
	}
	destructive := []string{
		"rm", "rmdir", "mv", "cp", "mkdir", "touch", "chmod", "chown", "chgrp", "ln", "tee", "truncate", "dd", "shred", ">", ">>",
		"npm install", "npm uninstall", "npm update", "npm ci", "npm link", "npm publish", "yarn add", "yarn remove", "yarn install", "yarn publish", "pnpm add", "pnpm remove", "pnpm install", "pnpm publish", "pip install", "pip uninstall", "apt install", "apt-get remove", "apt purge", "apt update", "apt upgrade", "brew install", "brew uninstall", "brew upgrade",
		"git add", "git commit", "git push", "git pull", "git merge", "git rebase", "git reset", "git checkout", "git branch -d", "git branch -D", "git stash", "git cherry-pick", "git revert", "git tag", "git init", "git clone", "sudo", "su", "kill", "pkill", "killall", "reboot", "shutdown", "systemctl start", "systemctl stop", "systemctl restart", "systemctl enable", "systemctl disable", "service foo start", "service foo stop", "service foo restart", "vi", "vim", "nano", "emacs", "code", "subl",
	}
	var probes []utilityProbe
	for _, command := range append(safe, destructive...) {
		for _, variant := range []string{command, strings.ToUpper(command), strings.ReplaceAll(command, "s", "ſ"), strings.ReplaceAll(command, "k", "K")} {
			for _, space := range []string{" ", "\t", "\v", "\u00a0", "\u0085", "\ufeff", "\u2028"} {
				text := strings.ReplaceAll(variant, " ", space)
				probes = append(probes, utilityProbe{"isSafeCommand", space + text}, utilityProbe{"isSafeCommand", "echo ok; " + text})
			}
		}
	}
	results := probeUpstreamUtilities(t, probes)
	for i, probe := range probes {
		want := string(results[i]) == "true"
		if got := IsSafeCommand(probe.Input); got != want {
			t.Fatalf("IsSafeCommand(%q) = %v; pinned upstream = %v", probe.Input, got, want)
		}
	}
}

func TestCleanStepTextBMPScalarCasingAgainstUpstream(t *testing.T) {
	var probes []utilityProbe
	for r := rune(0); r <= 0xffff; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue // Surrogates are not Unicode scalar values; this corpus tests first-code-unit casing only.
		}
		probes = append(probes, utilityProbe{"cleanStepText", string(r) + " ordinary step"})
	}
	results := probeUpstreamUtilities(t, probes)
	for i, probe := range probes {
		var want string
		if err := json.Unmarshal(results[i], &want); err != nil {
			t.Fatal(err)
		}
		if got := CleanStepText(probe.Input); got != want {
			t.Fatalf("CleanStepText(%q) = %q; pinned upstream = %q", probe.Input, got, want)
		}
	}
}

func TestExtractTodoItemsUnicodeCorpusAgainstUpstream(t *testing.T) {
	var probes []utilityProbe
	for _, lineBreak := range []string{"\n", "\r", "\r\n", "\u2028", "\u2029"} {
		for _, space := range []string{" ", "\t", "\v", "\ufeff", "\u00a0", "\u0085"} {
			for _, text := range []string{"Inspect the implementation", "Check" + space + "the result", "ßeta testing", "**Bold step text**", "`code example`", "Tiny", "😀 step text"} {
				probes = append(probes, utilityProbe{"extractTodoItems", "**Plan:**" + space + "\n" + "Comment*" + lineBreak + "1." + space + text + lineBreak + "2. Another ordinary step"})
			}
		}
	}
	results := probeUpstreamUtilities(t, probes)
	for i, probe := range probes {
		var want []TodoItem
		if err := json.Unmarshal(results[i], &want); err != nil {
			t.Fatal(err)
		}
		if got := ExtractTodoItems(probe.Input); !reflect.DeepEqual(got, want) {
			t.Fatalf("ExtractTodoItems(%q) = %+v; pinned upstream = %+v", probe.Input, got, want)
		}
	}
}
