package codingagent

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/utils/version-check.ts

// comparePackageVersions and isNewerPackageVersion over version spellings that node-semver accepts or rejects: a leading v or =, surrounding JS
// whitespace (NBSP, U+FEFF, line terminators), prerelease identifiers that are numeric, huge, zero-padded or alphanumeric, build metadata, components beyond
// Number.MAX_SAFE_INTEGER, missing or extra components and overlong strings.
func TestPackageVersionComparisonMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(223))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	number := func() string {
		if rng.Intn(4) != 0 {
			return pick("0", "1", "2", "3", "10", "9")
		}
		return pick("0", "1", "2", "10", "9", "01", "007", "123456789", "9007199254740991", "9007199254740992", "18446744073709551616", "-1", "1e3", "x", "")
	}
	identifier := func() string {
		if rng.Intn(3) != 0 {
			return pick("0", "1", "2", "10", "alpha", "beta", "rc", "a-b")
		}
		return pick("0", "1", "2", "10", "01", "alpha", "beta", "rc", "ALPHA", "a-b", "9007199254740990", "9007199254740991", "9007199254740992", "99999999999999999999", "-", "", "é", "1a", "0a", "00")
	}
	version := func() string {
		out := number() + "." + number() + "." + number()
		switch rng.Intn(8) {
		case 0:
			out = number() + "." + number()
		case 1:
			out += "." + number()
		}
		if rng.Intn(3) == 0 {
			parts := make([]string, 1+rng.Intn(3))
			for i := range parts {
				parts[i] = identifier()
			}
			out += "-" + strings.Join(parts, ".")
		}
		if rng.Intn(4) == 0 {
			parts := make([]string, 1+rng.Intn(2))
			for i := range parts {
				parts[i] = identifier()
			}
			out += "+" + strings.Join(parts, ".")
		}
		out = pick("", "", "", "", "", "", "v", "v", "V", "=", "=v", "vv", "v ") + out
		pad := func() string {
			return pick("", "", "", "", "", " ", "\t", "\n", "\u00a0", "\ufeff", "\u2028", "\u200b", "\u3000")
		}
		out = pad() + out + pad()
		if rng.Intn(30) == 0 {
			// The 256-character limit counts the leading v and the surrounding whitespace is already trimmed.
			out = pick("", "v", " v", "=") + "1.2.3-" + strings.Repeat("a", 247+rng.Intn(8)) + pick("", " ")
		}
		return out
	}
	pool := make([]string, 120)
	for i := range pool {
		pool[i] = version()
	}
	// Equal and nearly equal pairs are what the comparison and the not-semver fallback need; random pairs alone rarely produce them.
	type pair struct{ A, B string }
	var pairs []pair
	for range 4000 {
		a := pool[rng.Intn(len(pool))]
		b := pool[rng.Intn(len(pool))]
		if rng.Intn(5) == 0 {
			b = pick(" ", "", "\u00a0") + strings.TrimSpace(a) + pick(" ", "", "\n")
		}
		pairs = append(pairs, pair{a, b})
	}
	for n := 248; n <= 252; n++ {
		for _, prefix := range []string{"", "v", "=", "\u00a0v"} {
			long := prefix + "1.2.3-" + strings.Repeat("a", n)
			pairs = append(pairs, pair{long, "1.2.3"}, pair{"1.2.3", long}, pair{long, long})
		}
	}
	var want []struct {
		Cmp   *float64 `json:"cmp"`
		Newer bool     `json:"newer"`
	}
	pioracle.Run(t, `
const { comparePackageVersions, isNewerPackageVersion } = await load("pi-coding-agent/utils/version-check.js");
emit(input.map(({ A, B }) => ({ cmp: comparePackageVersions(A, B) ?? null, newer: isNewerPackageVersion(A, B) })));`, pairs, &want)
	compared, failures := 0, 0
	for i, p := range pairs {
		cmp, ok := ComparePackageVersions(p.A, p.B)
		newer := IsNewerPackageVersion(p.A, p.B)
		wantOK := want[i].Cmp != nil
		if ok {
			compared++
		}
		if ok != wantOK || (ok && float64(cmp) != *want[i].Cmp) || newer != want[i].Newer {
			if failures++; failures <= 12 {
				got := "undefined"
				if ok {
					got = fmt.Sprint(cmp)
				}
				wantText := "undefined"
				if wantOK {
					wantText = fmt.Sprint(*want[i].Cmp)
				}
				t.Errorf("%q vs %q: PiG compare=%s newer=%v, Pi compare=%s newer=%v", p.A, p.B, got, newer, wantText, want[i].Newer)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d pairs differ", failures, len(pairs))
	}
	if compared < len(pairs)/10 {
		t.Fatalf("only %d of %d pairs were comparable", compared, len(pairs))
	}
}
