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

// Math.exp of the Node that runs the pinned Pi, compared bit for bit: special values, the overflow and underflow thresholds, subnormal results, arguments next to fdlibm's reduction thresholds, softmax-shaped arguments (non-positive, a log-probability difference) and random doubles of every exponent.
func TestExpMatchesNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261012))
	inputs := []float64{0, math.Copysign(0, -1), 1, -1, 2, 0.5, -0.5, math.E, 10, 5e-324, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN(), 709.782712893384, 709.7827128933841, -745.1332191019411, -745.1332191019412, -708.3964185322641, -1e30, 1e-10, 1 + 0x1p-30, 0.34657359027997264, 0.3465735902799727, 1.0397207708399179, 1.039720770839918}
	for range 40000 {
		inputs = append(inputs,
			math.Float64frombits(rng.Uint64()),
			-rng.Float64()*50,
			-rng.Float64()*745,
			(rng.Float64()-0.5)*20,
			(rng.Float64()-0.5)*1400,
			-float64(rng.Intn(2000))/float64(1+rng.Intn(50)),
			(rng.Float64()-0.5)*0x1p-27,
			math.Nextafter(float64(rng.Intn(1500)-750)*0.34657359027997264, 0),
		)
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
process.stdout.write(JSON.stringify(input.map((h) => { b[0] = BigInt("0x" + h); f[0] = Math.exp(f[0]); return b[0].toString(16).padStart(16, "0"); })));
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
	bad, goBad := 0, 0
	for i, v := range inputs {
		got := Exp(v)
		gotBits := fmt.Sprintf("%016x", math.Float64bits(got))
		var wantBits uint64
		if _, err := fmt.Sscanf(want[i], "%x", &wantBits); err != nil {
			t.Fatal(err)
		}
		if math.IsNaN(got) && math.IsNaN(math.Float64frombits(wantBits)) {
			continue // JavaScript has one NaN value; its payload bits are not observable through Math.exp's callers.
		}
		if math.Float64bits(math.Exp(v)) != wantBits {
			goBad++
		}
		if gotBits != want[i] {
			if bad++; bad <= 6 {
				t.Errorf("Exp(%v) = %s (%v), Node %s", v, gotBits, got, want[i])
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d results differ from Node", bad, len(inputs))
	}
	// math.Exp differing from Node on this grid is why Exp exists; a grid it never differs on would prove nothing.
	if goBad == 0 {
		t.Errorf("math.Exp agrees with Node on all %d inputs, so the comparison cannot tell Exp from it", len(inputs))
	}
}
