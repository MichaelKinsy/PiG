//go:build !pig_strip_bedrock_converse_stream

package bedrock

// pi: packages/ai/src/api/bedrock-converse-stream.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// reservedHeaderNames spell header names with the characters whose case mapping differs between Go and JavaScript: U+0130 (JS keeps "i" plus a combining dot), U+212A KELVIN SIGN (both lower it to
// "k"), U+017F LONG S (simple case folding equates it with "s", toLowerCase does not) and an upper-case sigma.
func reservedHeaderNames() []string {
	random := rand.New(rand.NewSource(20260955))
	bases := []string{"Read", "Write", "Edit", "Bash", "Grep", "Glob", "AskUserQuestion", "EnterPlanMode", "KillShell", "NotebookEdit", "Skill", "Task", "TodoWrite", "WebFetch", "WebSearch",
		"host", "Host", "authorization", "Authorization", "x-amz-date", "X-Amz-Security-Token", "x-opencode-session", "X-OpenCode-Session", "https://api.deepseek.com/v1", "https://API.DeepSeek.COM/v1", "https://x.example/deepseek.com", "deepseek"}
	special := []string{"\u0130", "\u212a", "\u017f", "\u03a3", "\u0131", "\u00cf", "I", "i", "S", "s", "K", "k"}
	lookalikes := map[rune]rune{'i': '\u0130', 's': '\u017f', 'k': '\u212a'}
	spell := func(base string) string {
		runes := []rune(base)
		for range random.Intn(4) {
			if len(runes) == 0 {
				break
			}
			at := random.Intn(len(runes))
			switch random.Intn(4) {
			case 3:
				for _, candidate := range random.Perm(len(runes)) {
					if replacement, ok := lookalikes[runes[candidate]|0x20]; ok {
						runes[candidate] = replacement
						break
					}
				}
			case 0:
				runes[at], _ = utf8.DecodeRuneInString(special[random.Intn(len(special))])
			case 1:
				if runes[at] >= 'a' && runes[at] <= 'z' {
					runes[at] -= 'a' - 'A'
				} else if runes[at] >= 'A' && runes[at] <= 'Z' {
					runes[at] += 'a' - 'A'
				}
			default:
				runes = append(runes[:at], append([]rune(special[random.Intn(len(special))]), runes[at:]...)...)
			}
		}
		return string(runes)
	}
	var names []string
	for range 1500 {
		base := bases[random.Intn(len(bases))]
		name := spell(base)
		names = append(names, name)
	}
	return names
}

// The private isReservedHeader cut from Pi runs in Node on the same spellings: it compares with toLowerCase, which Go's ToLower and EqualFold map differently for U+0130 and U+017F.
func TestBedrockReservedHeaderMatchesPi(t *testing.T) {
	names := reservedHeaderNames()
	payload, err := json.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "api", "bedrock-converse-stream.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/bedrock_reserved_header.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []bool
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(names) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(names))
	}
	differing, positives := 0, 0
	for i, name := range names {
		if got := isReservedBedrockHeader(name); got != expected[i] {
			if differing++; differing <= 12 {
				t.Errorf("reserved %q: Pig %v, Pi %v", name, got, expected[i])
			}
		}
		if expected[i] {
			positives++
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(names))
	}
	if positives < 20 {
		t.Errorf("reserved probes are one-sided: %d positive answers", positives)
	}
}
