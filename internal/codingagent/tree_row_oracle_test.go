package codingagent

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's TreeList.getEntryDisplayText (modes/interactive/components/tree-selector.js) runs against the vendored Pi module for chains of session entries: user, assistant (text, tool calls, aborted, error), tool result, bash, custom, branch-summary, compaction and metadata entries with ASCII, CJK and astral text across the 50, 80, 200 and 40 length limits.
func TestTreeRowFormatMatchesPiOracle(t *testing.T) {
	useTreeOracleTUIDefaults(t)
	t.Setenv("HOME", "/home/oracle")
	t.Setenv("USERPROFILE", "")
	chains := treeOracleChains()
	var want [][]string
	pioracle.Run(t, `
const th = await load("pi-coding-agent/modes/interactive/theme/theme.js");
th.initTheme("dark", false);
const mod = await load("pi-coding-agent/modes/interactive/components/tree-selector.js");
emit(input.map((chain) => {
	const nodes = chain.map((entry) => ({ entry, children: [], label: undefined }));
	nodes.forEach((node, i) => { if (nodes[i + 1]) node.children.push(nodes[i + 1]); });
	const c = new mod.TreeSelectorComponent([nodes[0]], chain[chain.length - 1].id, 20, () => {}, () => {}, () => {});
	return nodes.map((node) => c.treeList.getEntryDisplayText(node, false));
}));`, chains, &want, treeOracleEnv...)
	mismatches := 0
	for i, chain := range chains {
		nodes := make([]*SessionTreeNode, len(chain))
		for j, fields := range chain {
			nodes[j] = &SessionTreeNode{Entry: mustEntry(t, fields)}
			if j > 0 {
				nodes[j-1].Children = []*SessionTreeNode{nodes[j]}
			}
		}
		leaf := chain[len(chain)-1]["id"].(string)
		formatter := newTreeRowFormatterFromTree(nodes[:1], &leaf)
		for j, node := range nodes {
			// Node writes a lone surrogate as U+FFFD; Go keeps it as WTF-8 bytes.
			if got := strings.ToValidUTF8(formatter.FormatTreeRow(node.Entry), "\ufffd"); got != want[i][j] {
				if mismatches++; mismatches <= 12 {
					t.Errorf("%v\n Go %q\n Pi %q", chain[j], got, want[i][j])
				}
			}
		}
	}
	if mismatches > 12 {
		t.Errorf("%d mismatching rows", mismatches)
	}
}

// treeOracleChains is the corpus of session-entry chains shared by the tree-selector oracle tests: user, assistant (text, tool calls, aborted, error), tool result, bash, custom, branch-summary, compaction and metadata entries.
func treeOracleChains() [][]map[string]any {
	rng := rand.New(rand.NewSource(9))
	texts := []string{
		"", "hello", "  padded \n\ttabs\n ", "line1\nline2", strings.Repeat("a", 199), strings.Repeat("a", 200), strings.Repeat("a", 201), strings.Repeat("小", 150), strings.Repeat("小", 201),
		strings.Repeat("😀", 99) + "x", strings.Repeat("😀", 100), strings.Repeat("😀", 101), "x" + strings.Repeat("😀", 100), "\u00a0nbsp\u00a0", "\ufeffbom\ufeff", "\u2003em\u2003", "a\u0085b\u0085",
	}
	text := func() string { return texts[rng.Intn(len(texts))] }
	blocks := func(s string) any {
		switch rng.Intn(3) {
		case 0:
			return s
		case 1:
			return []any{map[string]any{"type": "text", "text": s}}
		default:
			runes := []rune(s)
			return []any{map[string]any{"type": "text", "text": string(runes[:len(runes)/2])}, map[string]any{"type": "image", "data": "AA", "mimeType": "image/png"}, map[string]any{"type": "text", "text": string(runes[len(runes)/2:])}}
		}
	}
	cmds := []string{"ls", "echo hi\n\tthere", strings.Repeat("c", 50), strings.Repeat("c", 51), strings.Repeat("小", 50), strings.Repeat("小", 51), strings.Repeat("😀", 25), strings.Repeat("😀", 26), "  spaced " + strings.Repeat("x", 60), strings.Repeat("\n", 60) + "x", ""}
	pathArgs := []string{"/home/oracle/a.go", "/home/oraclex/b", "/other", ""}
	toolCall := func(id string) map[string]any {
		switch rng.Intn(8) {
		case 0:
			return map[string]any{"type": "toolCall", "id": id, "name": "bash", "arguments": map[string]any{"command": cmds[rng.Intn(len(cmds))]}}
		case 1:
			return map[string]any{"type": "toolCall", "id": id, "name": "read", "arguments": map[string]any{"path": pathArgs[rng.Intn(len(pathArgs))], "offset": 5, "limit": 10}}
		case 2:
			return map[string]any{"type": "toolCall", "id": id, "name": "read", "arguments": map[string]any{"file_path": pathArgs[rng.Intn(len(pathArgs))], "limit": 3}}
		case 3:
			return map[string]any{"type": "toolCall", "id": id, "name": "grep", "arguments": map[string]any{"pattern": "x+", "path": pathArgs[rng.Intn(len(pathArgs))]}}
		case 4:
			return map[string]any{"type": "toolCall", "id": id, "name": "ls", "arguments": map[string]any{}}
		case 5:
			return map[string]any{"type": "toolCall", "id": id, "name": "find", "arguments": map[string]any{"pattern": "*.go"}}
		case 6:
			return map[string]any{"type": "toolCall", "id": id, "name": "write", "arguments": map[string]any{"path": pathArgs[rng.Intn(len(pathArgs))], "content": "c"}}
		default:
			return map[string]any{"type": "toolCall", "id": id, "name": "custom_tool", "arguments": map[string]any{"query": text(), "n": 1, "z": []any{1, 2}}}
		}
	}
	usage := map[string]any{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}
	var chains [][]map[string]any
	for c := range 260 {
		var chain []map[string]any
		add := func(fields map[string]any) {
			fields["id"] = fmt.Sprintf("c%d-%d", c, len(chain))
			fields["parentId"] = nil
			if len(chain) > 0 {
				fields["parentId"] = chain[len(chain)-1]["id"]
			}
			fields["timestamp"] = "2026-01-01T00:00:00.000Z"
			chain = append(chain, fields)
		}
		for range 1 + rng.Intn(3) {
			switch rng.Intn(11) {
			case 0:
				add(map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": blocks(text()), "timestamp": 1}})
			case 1, 2, 3:
				content := []any{}
				if s := text(); s != "" && rng.Intn(3) != 0 {
					content = append(content, map[string]any{"type": "text", "text": s})
				}
				callID := fmt.Sprintf("call-%d-%d", c, len(chain))
				if rng.Intn(2) == 0 {
					content = append(content, toolCall(callID))
				}
				message := map[string]any{"role": "assistant", "content": content, "api": "x", "provider": "p", "model": "m", "usage": usage, "stopReason": []string{"stop", "toolUse", "aborted", "error", "length"}[rng.Intn(5)], "timestamp": 1}
				if rng.Intn(2) == 0 {
					message["errorMessage"] = text()
				}
				add(map[string]any{"type": "message", "message": message})
				if len(content) > 0 {
					if call, ok := content[len(content)-1].(map[string]any); ok && call["type"] == "toolCall" {
						add(map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": call["name"], "content": []any{}, "isError": false, "timestamp": 1}})
					}
				}
			case 4:
				add(map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolCallId": "missing", "toolName": "read", "content": []any{}, "isError": false, "timestamp": 1}})
			case 5:
				add(map[string]any{"type": "message", "message": map[string]any{"role": "bashExecution", "command": cmds[rng.Intn(len(cmds))], "output": "o", "exitCode": 0, "cancelled": false, "truncated": false, "timestamp": 1}})
			case 6:
				add(map[string]any{"type": "custom_message", "customType": "ct", "content": blocks(text()), "display": true})
			case 7:
				add(map[string]any{"type": "branch_summary", "fromId": "x", "summary": text()})
			case 8:
				add(map[string]any{"type": "compaction", "summary": "s", "firstKeptEntryId": "x", "tokensBefore": []int{0, 499, 500, 1500, 123456}[rng.Intn(5)]})
			case 9:
				add(map[string]any{"type": "session_info", "name": []string{"", "named"}[rng.Intn(2)]})
			default:
				switch rng.Intn(4) {
				case 0:
					add(map[string]any{"type": "model_change", "provider": "p", "modelId": "m1"})
				case 1:
					add(map[string]any{"type": "thinking_level_change", "thinkingLevel": "high"})
				case 2:
					add(map[string]any{"type": "label", "targetId": "x", "label": nil})
				default:
					add(map[string]any{"type": "custom", "customType": "ext"})
				}
			}
		}
		chains = append(chains, chain)
	}
	for i, cmd := range cmds {
		id := fmt.Sprintf("bash-%d", i)
		call := map[string]any{"type": "toolCall", "id": "tc" + id, "name": "bash", "arguments": map[string]any{"command": cmd}}
		assistant := map[string]any{"type": "message", "id": id + "a", "parentId": nil, "timestamp": "2026-01-01T00:00:00.000Z", "message": map[string]any{"role": "assistant", "content": []any{call}, "api": "x", "provider": "p", "model": "m", "usage": usage, "stopReason": "toolUse", "timestamp": 1}}
		result := map[string]any{"type": "message", "id": id + "r", "parentId": id + "a", "timestamp": "2026-01-01T00:00:00.000Z", "message": map[string]any{"role": "toolResult", "toolCallId": "tc" + id, "toolName": "bash", "content": []any{}, "isError": false, "timestamp": 1}}
		chains = append(chains, []map[string]any{assistant, result})
	}
	return chains
}

// useTreeOracleTUIDefaults installs Pi's default keybindings, the dark theme, truecolor output and legacy (non-Kitty) key parsing for the test: earlier tests in the package leave other bindings and capabilities installed.
func useTreeOracleTUIDefaults(t *testing.T) {
	t.Helper()
	previousKeybindings, previousCaps, previousKitty := tui.GetTUIKeybindings(), tui.GetCapabilities(), tui.IsKittyProtocolActive()
	t.Cleanup(func() {
		tui.SetKeybindings(previousKeybindings)
		tui.SetCapabilities(previousCaps)
		tui.SetKittyProtocolActive(previousKitty)
	})
	tui.SetKittyProtocolActive(false)
	tui.SetKeybindings(NewKeybindingsManagerFromBindings(nil, "").merged)
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
}

// treeOracleEnv is the Pi side of useTreeOracleTUIDefaults. Pi picks the theme's colour mode from the detected terminal
// (terminal-image.ts getTerminalColorMode), not FORCE_COLOR, and the oracle inherits the test's environment: under Kitty
// or Ghostty it is truecolor, in a plain xterm or tmux without COLORTERM it is 256-colour. COLORTERM=truecolor gives Pi
// the truecolor terminal the Go side installs, as tool_call_args_oracle_test.go does, so the host's terminal never decides
// the comparison.
var treeOracleEnv = []string{"HOME=/home/oracle", "USERPROFILE=", "FORCE_COLOR=3", "COLORTERM=truecolor"}
