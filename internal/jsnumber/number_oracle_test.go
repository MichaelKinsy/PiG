package jsnumber

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// pi: Number(text), String(number) and JSON.stringify(number) of the Node that runs the pinned Pi.
func TestNumberConversionsMatchNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	var doubles []string
	addDouble := func(v float64) { doubles = append(doubles, fmt.Sprintf("%016x", math.Float64bits(v))) }
	for _, v := range []float64{0, math.Copysign(0, -1), 1, -1, 0.1, 1e21, 1e-7, 123456789012345680000, 1e-6, 5e-324, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN(), 1.5, 100, 1e20, 0.000001, 0.0000001, 4.35, 0.3, 2e-7, 1.7976931348623157e308} {
		addDouble(v)
	}
	for range 6000 {
		addDouble(math.Float64frombits(rng.Uint64()))
		addDouble(float64(rng.Int63n(1<<53)) * math.Pow10(rng.Intn(50)-25))
		addDouble(float64(rng.Intn(2000000)-1000000) / math.Pow10(rng.Intn(12)))
	}
	pieces := []string{"0", "1", "9", "00", "0x", "0X1f", "0b101", "0o17", "0b2", "0xg", ".", "e", "E", "+", "-", "_", "1_0", " ", "\t", "\n", "\u00a0", "\ufeff", "\u2028", "\u3000", "\u180e", "Infinity", "-Infinity", "+Infinity", "infinity", "NaN", "1e", "1e+", "1e1000", "1e-1000", ".e1", "5.", ".5", "0.", "\x00", "ａ", "١", "n", "123456789012345678901234567890"}
	var texts []string
	for range 12000 {
		var b strings.Builder
		for range 1 + rng.Intn(4) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		texts = append(texts, b.String())
	}
	for range 3000 {
		texts = append(texts, strconv.FormatFloat(math.Float64frombits(rng.Uint64()), "eEfg"[rng.Intn(4)], -1, 64))
	}
	in, _ := json.Marshal(map[string]any{"doubles": doubles, "texts": texts})
	cmd := exec.CommandContext(t.Context(), "node", "testdata/number.mjs")
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var want struct {
		Strings, JSON, Numbers []string
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	var differences []string
	for i, hex := range doubles {
		bits, _ := strconv.ParseUint(hex, 16, 64)
		value := math.Float64frombits(bits)
		if got := String(value); got != want.Strings[i] {
			differences = append(differences, fmt.Sprintf("String(%s) = %q, Node %q", hex, got, want.Strings[i]))
		}
		wantJSON := want.JSON[i]
		if wantJSON == "null" || !math.IsNaN(value) && !math.IsInf(value, 0) {
			if got := string(JSON(value)); got != wantJSON {
				differences = append(differences, fmt.Sprintf("JSON(%s) = %q, Node %q", hex, got, wantJSON))
			}
		}
	}
	for i, text := range texts {
		value := Parse(text)
		got := "nan"
		if !math.IsNaN(value) {
			got = fmt.Sprintf("%016x", math.Float64bits(value))
		}
		if got != want.Numbers[i] {
			differences = append(differences, fmt.Sprintf("Parse(%q) = %s, Node %s", text, got, want.Numbers[i]))
		}
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 15 {
			shown = shown[:15]
		}
		t.Errorf("%d differences from Node:\n%s", len(differences), strings.Join(shown, "\n"))
	}
}
