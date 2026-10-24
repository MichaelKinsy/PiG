package codingagent

// pi: packages/coding-agent/src/core/key-id-schema.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// keyIDCandidates are the strings both validators answer: every run of zero to five modifiers (repeats and every order) before each base key of Pi's
// `Key`, before the single characters a-z, 0-9 and near misses (upper case, two characters, a trailing newline, a function key past f12), plus malformed
// modifier text.
func keyIDCandidates() []string {
	bases := append([]string(nil), keyIDBaseKeyList()...)
	for c := 'a'; c <= 'z'; c++ {
		bases = append(bases, string(c))
	}
	for c := '0'; c <= '9'; c++ {
		bases = append(bases, string(c))
	}
	bases = append(bases, "", " ", "A", "Z", "ab", "f13", "f0", "F1", "Enter", "pageup", "ctrl", "shift", "+", "++", "a\n", "a ", " a", "é", "日", "\x00", "meta", "cmd")
	prefixes := []string{""}
	level := []string{""}
	for range 5 {
		var next []string
		for _, p := range level {
			for _, m := range keyIDModifiers {
				next = append(next, p+m+"+")
			}
		}
		prefixes = append(prefixes, next...)
		level = next
	}
	var out []string
	for _, p := range prefixes {
		for _, b := range bases {
			out = append(out, p+b)
		}
	}
	out = append(out, "ctrl+", "ctrl+shift+", "xctrl+a", "ctrl+xshift+a", "Ctrl+a", "CTRL+a", "ctrl +a", "ctrl+a+b", "ctrl+ctrl", "ctrl++", "ctrl+shift++", "alt+alt+", "ctrl+\n", "super+super+x", "ctrl+f1+f2")
	return out
}

// Pi checks every keybindings.json value with Compile(KeybindingValueSchema) (keybindings.ts toKeybindingsConfig): a KeyId string or an array of them.
// isKeyID and decodeKeybindingsConfig must accept and reject exactly what that compiled schema does.
func TestKeyIDValidationMatchesPiKeyIdSchema(t *testing.T) {
	strs := keyIDCandidates()
	values := make([]any, 0, len(strs)+20)
	for _, s := range strs {
		values = append(values, s)
	}
	values = append(values,
		[]any{}, []any{"ctrl+x"}, []any{"ctrl+x", "alt+x"}, []any{"ctrl+x", "Alt+x"}, []any{"ctrl+x", 1}, []any{"bogus"}, []any{[]any{"a"}}, []any{nil},
		nil, 1, 1.5, true, false, map[string]any{"a": 1}, map[string]any{}, "", []any{""},
	)
	payload, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/key_id_check.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi struct {
		Value []bool `json:"value"`
		ID    []bool `json:"id"`
	}
	if err := json.Unmarshal(output, &pi); err != nil {
		t.Fatal(err)
	}
	if len(pi.Value) != len(values) || len(pi.ID) != len(values) {
		t.Fatalf("Pi answered %d and %d of %d values", len(pi.Value), len(pi.ID), len(values))
	}
	accepted, rejected := 0, 0
	for i, value := range values {
		text, isString := value.(string)
		if isString {
			if got := isKeyID(text); got != pi.ID[i] {
				t.Errorf("isKeyID(%q) = %v, Pi's KeyIdSchema says %v", text, got, pi.ID[i])
			}
			if pi.ID[i] {
				accepted++
			} else {
				rejected++
			}
		}
		_, kept := decodeKeybindingsConfig(map[string]any{"action": value})["action"]
		if kept != pi.Value[i] {
			encoded, _ := json.Marshal(value)
			t.Errorf("keybinding value %s kept = %v, Pi's KeybindingValueSchema says %v", strings.TrimSpace(string(encoded)), kept, pi.Value[i])
		}
	}
	if accepted < 100 || rejected < 1000 {
		t.Fatalf("the candidates exercise %d valid and %d invalid key ids: too few of either to prove the grammar", accepted, rejected)
	}
}
