package jsmath

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
)

// Math.log of the Node that runs the pinned Pi, compared bit for bit: special values, subnormals, values next to 1 (both near-1 branches), mantissas next to fdlibm's branch thresholds, BM25-shaped quotients and random doubles of every exponent.
func TestLogMatchesNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261011))
	inputs := []float64{0, math.Copysign(0, -1), 1, -1, 2, 0.5, math.E, 10, 5e-324, 2.2250738585072014e-308, 2.225073858507201e-308, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN(), math.Nextafter(1, 2), math.Nextafter(1, 0), 1 + 0x1p-21, 1 - 0x1p-21, 1 + 0x1p-20, 1.4142135623730951, 0.7071067811865476}
	for range 40000 {
		inputs = append(inputs,
			math.Float64frombits(rng.Uint64()&^(1<<63)),
			1+rng.Float64()*1000,
			1+(float64(rng.Intn(5000))+0.5)/(float64(rng.Intn(5000))+0.5),
			1+float64(rng.Int63n(1<<20)-1<<19)*0x1p-40,
			math.Ldexp(1+float64(rng.Int63n(1<<12)-1<<11)*0x1p-52, rng.Intn(2100)-1074),
			math.Float64frombits(rng.Uint64()&(1<<52-1)),
		)
		// High mantissa words next to fdlibm's thresholds: 0x6a09c (sqrt(2), the x or x/2 normalization) and 0x6147a/0x6b851 (the two polynomial forms).
		for _, edge := range []int64{0x6a09c, 0x6147a, 0x6b851} {
			high := uint64(edge+rng.Int63n(0x200)-0x100) & 0xfffff
			exponent := uint64(rng.Intn(2046) + 1)
			inputs = append(inputs, math.Float64frombits(exponent<<52|high<<32|uint64(rng.Uint32())))
		}
	}
	hexes := make([]string, len(inputs))
	for i, v := range inputs {
		hexes[i] = fmt.Sprintf("%016x", math.Float64bits(v))
	}
	payload, err := json.Marshal(hexes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "-e", `
const input = JSON.parse(require("fs").readFileSync(0, "utf8"));
const b = new BigUint64Array(1), f = new Float64Array(b.buffer);
process.stdout.write(JSON.stringify(input.map((h) => { b[0] = BigInt("0x" + h); f[0] = Math.log(f[0]); return b[0].toString(16).padStart(16, "0"); })));
`)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	bad := 0
	for i, v := range inputs {
		got := Log(v)
		gotBits := fmt.Sprintf("%016x", math.Float64bits(got))
		var wantBits uint64
		if _, err := fmt.Sscanf(want[i], "%x", &wantBits); err != nil {
			t.Fatal(err)
		}
		if math.IsNaN(got) && math.IsNaN(math.Float64frombits(wantBits)) {
			continue // JavaScript has one NaN value; its payload bits are not observable through Math.log's callers.
		}
		if gotBits != want[i] {
			if bad++; bad <= 6 {
				t.Errorf("Log(%v) = %s (%v), Node %s", v, gotBits, got, want[i])
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d results differ from Node", bad, len(inputs))
	}
}
