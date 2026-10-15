package jsonstringify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
)

// pi: JSON.stringify(JSON.parse(text)) of the Node that runs the pinned Pi: member order (integer-like keys first), duplicate keys, number forms, string escapes including lone surrogates.
func TestCanonicalizeMatchesNodeJSON(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	keys := []string{"a", "b", "z", "1", "0", "10", "2", "01", "-1", "1.5", "4294967294", "4294967295", "4294967296", "__proto__", "constructor", "", " ", "é", "日本", "😀", "\\ud800", "\\udc00", "\\u2028", "a\\nb", "\\\"q\\\"", "\\/", "A"}
	strs := []string{"", "x", "é", "日本語", "😀", "\\ud800", "\\udc00", "\\ud83d\\ude00", "\\ud83d", "a\\nb\\tc\\r", "\\u0000\\u001f\\u007f", "\\u2028\\u2029", "\\\"\\\\\\/\\b\\f", "\\u00e9", "\\uFFFF", "\\u0041", "x\\u0000y", "\ufeff", "\u2028"}
	nums := []string{"0", "-0", "1", "-1", "1.5", "1e21", "1E+21", "1e-7", "1e-6", "0.000001", "123456789012345678901234567890", "1.7976931348623157e308", "5e-324", "1e400", "-1e400", "0.1", "100", "1e2", "1.0", "1.10", "9007199254740993", "-9007199254740993", "0e5", "12345678901234567890.123"}
	var value func(depth int) string
	value = func(depth int) string {
		switch k := rng.Intn(10); {
		case k < 3 || depth > 3:
			return `"` + strs[rng.Intn(len(strs))] + `"`
		case k < 5:
			return nums[rng.Intn(len(nums))]
		case k == 5:
			return []string{"true", "false", "null"}[rng.Intn(3)]
		case k < 8:
			var members []string
			for range rng.Intn(6) {
				members = append(members, `"`+keys[rng.Intn(len(keys))]+`":`+value(depth+1))
			}
			return "{" + strings.Join(members, pick(rng, []string{",", " , ", ",\n"})) + "}"
		default:
			var items []string
			for range rng.Intn(5) {
				items = append(items, value(depth+1))
			}
			return "[" + strings.Join(items, pick(rng, []string{",", " , "})) + "]"
		}
	}
	var texts []string
	for range 6000 {
		texts = append(texts, pick(rng, []string{"", " ", "\n"})+value(0)+pick(rng, []string{"", " ", "\n"}))
	}
	in, _ := json.Marshal(texts)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/stringify.mjs")
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var want []string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	var differences []string
	reordered := 0
	for i, text := range texts {
		got, err := Canonicalize([]byte(text))
		if err != nil || string(got) != want[i] {
			differences = append(differences, fmt.Sprintf("input %q\n  go: %q %v\n  js: %q", text, got, err, want[i]))
		}
		if strings.Contains(want[i], `"0":`) || strings.Contains(want[i], `"1":`) {
			reordered++
		}
	}
	if reordered < 100 {
		t.Fatalf("only %d inputs hold integer-like keys", reordered)
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 8 {
			shown = shown[:8]
		}
		t.Errorf("%d of %d inputs differ from JSON.stringify(JSON.parse()):\n%s", len(differences), len(texts), strings.Join(shown, "\n"))
	}
}

func pick(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }
