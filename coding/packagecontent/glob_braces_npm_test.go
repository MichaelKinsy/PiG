package packagecontent

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Rows recorded from braceExpand of Pi's npm minimatch 10.2.6 (brace-expansion 5.0.12) under Node 24.19. Node's bundled copy, which braceExpand ports, gives fewer results for each: after a "{a},b}" rewrite it drops every empty or literal value of a set that is not first, where 5.0.12 drops only empty results of a first comma set. 95 of 200,000 random brace patterns differ this way.
func TestNpmBraceExpandFollowsBraceExpansion5(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{"{a.md,}{},,}", []string{"a.md}", "a.md", "a.md", "}"}},
		{"{a,b}{},}", []string{"a}", "a", "b}", "b"}},
		{"{,a}{}a{},}", []string{"}a{}", "a}a{}", "a"}},
		{"{a,}{}},,3}", []string{"a}}", "a", "a3", "}}", "3"}},
		{"{,a}{}{,},,}", []string{"}", "}", "a}", "a}", "a", "a"}},
		{`..{a,}{}3\{a,}`, []string{"..a}3{a", "..a", "..}3{a", ".."}},
		{"{1..3}{1},}", []string{"11}", "1", "21}", "2", "31}", "3"}},
		{"${x}{a,}{}b,}", []string{"${x}a}b", "${x}a", "${x}}b", "${x}"}},
		{"{,a}{}$,,{a,b}}", []string{"}$", "a", "b", "a}$", "a", "aa", "ab"}},
		{",{a..c}{}\\{,}", []string{",a}{", ",a", ",b}{", ",b", ",c}{", ",c"}},
		// Rows both copies agree on.
		{"{a,b}", []string{"a", "b"}},
		{"{,}", []string{}},
		{"a{,}", []string{"a", "a"}},
		{"{a},b}", []string{"a}", "b"}},
		{"{Z..a}", []string{"Z", "[", "", "]", "^", "_", "`", "a"}},
		{"{-01..5}", []string{"-01", "000", "001", "002", "003", "004", "005"}},
		// A padded step pads too.
		{"{1..10..03}", []string{"01", "04", "07", "10"}},
		{"{-1..10..03}", []string{"-1", "02", "05", "08"}},
		{"{1..3..02}", []string{"1", "3"}},
		// A leading "{}" is literal.
		{"{}a,b}", []string{"{}a,b}"}},
		{"{{a,b}}", []string{"{a}", "{b}"}},
		{"a{},b}c", []string{"a}c", "abc"}},
		{"${a,b}{c,d}", []string{"${a,b}c", "${a,b}d"}},
		{"x{1..3}y", []string{"x1y", "x2y", "x3y"}},
		{"plain", []string{"plain"}},
	} {
		if got := npmBraceExpand(tc.pattern); !slices.Equal(got, tc.want) {
			t.Errorf("npmBraceExpand(%q) = %q, want %q", tc.pattern, got, tc.want)
		}
	}
}

// brace-expansion 5.0.12 bounds what Node's bundled copy does not: EXPANSION_MAX results, EXPANSION_MAX_LENGTH (4,000,000) UTF-16 units in one result or value set, EXPANSION_MAX_DEPTH (1,000) levels of nesting, after which the rest is literal, and EXPANSION_MAX_REWRITES (1,000) "{a},b}" rewrites. A sequence stops at EXPANSION_MAX items, so one whose step no longer advances a JavaScript number ends there instead of throwing "Invalid array length" as Node's copy does. Counts, total lengths and SHA-256 of the newline-joined results recorded from Pi's npm minimatch 10.2.6 under Node 24.19; none of these patterns holds an escape, so the random escape markers cannot shift a limit.
func TestNpmBraceExpandLimitsFollowBraceExpansion5(t *testing.T) {
	nest := func(n int, pre, post string) string {
		return pre + strings.Repeat("{", n) + "a,b" + strings.Repeat("}", n) + post
	}
	rewrite := func(n int) string { return "{a}" + strings.Repeat("}", n) + ",z}" }
	for _, tc := range []struct {
		name, pattern string
		count, total  int
		sha           string
	}{
		{"depth 999", nest(999, "", ""), 2, 3994, "43049583bf21d963d572cfab03959220b64aabdc8e410d5dbc6cfd255ca2790b"},
		{"depth 1000", nest(1000, "", ""), 2, 3998, "850304080d043bbc049beb992c77d858744d193bacc860f5ece4bc0503911222"},
		{"depth 1001", nest(1001, "", ""), 2, 4002, "f8564f4e9e6a95f0fa48010f22b8645b386b820bd2142db31c8c8bca99811412"},
		{"depth 1002", nest(1002, "", ""), 1, 2007, "4b7d840f84e15279ea38e90d2301a06f93f27b0f005021377235137f9f130dac"},
		{"depth 1001 inside text", nest(1001, "x", "y"), 2, 4006, "5b77e23f799722125c3006032783b0eb1b7d0e8af11a28f5014a6491e70dc36b"},
		{"depth 1002 inside text", nest(1002, "x", "y"), 1, 2009, "5df6e8dd49012284f87075e6984d7a4846693f9aa1b6d4e522296c5416c39d95"},
		{"999 rewrites", rewrite(999), 2, 1002, "3c31edc643b758e6a1db2cc3f44bc860cf62e0c1a5c2cc8e08c2f90ed0769392"},
		{"1000 rewrites", rewrite(1000), 1, 1006, "ec0481bdc499ec87bdb7423af06c363205de5ad5642b4d689cb9b35870c9ba61"},
		{"300 groups", strings.Repeat("{a,b}", 300), 13333, 3999900, "f4a043562a2207b8c433a78f051315153c0f0fa6fe473944149ebcbd30ef205b"},
		{"long results", strings.Repeat("x", 40) + "{10000..99999}", 88888, 3999960, "4d68ee729f2d89624d3967b2a087b753a0fcacd6ef8074516c49bdc58435be53"},
		{"long values", "{" + strings.Repeat("a", 60) + "{1..99999},b}", 61710, 3999980, "1ba6d36de62ccfcece3afef2e594c5078c689090251603ed59a2ed0693426813"},
		// The values stop below the limit, so the next prefix still fits with the short first value: "xc" comes last.
		{"capped values", "{,x}{c," + strings.Repeat("a", 60) + "{1..99999}," + strings.Repeat("b", 60) + "{1..99999}}", 61711, 3999982, "a7f95efef480c96643809551915f0e8b11fd9ef3715051fa6a529452620c7256"},
		{"padded sequence", "{" + strings.Repeat("0", 49) + "1..99999}", 80000, 4000000, "3a0eb81ed7233b3fdc77845c6bbe426f7f75a1435175ab0b1a495a75269b7522"},
		// Empty values of a first comma set are skipped before they count against EXPANSION_MAX. After a prefix they count: "x" twice, then x1 to x99998.
		{"empty values skipped", "{,,{1..99999}}", 99999, 488889, "59c105e98516f0820eb1d5564d80a1cddccb27ad587437686a4be2c5c86aefff"},
		{"empty values counted", "x{,,{1..99999}}", 100000, 588884, "040bfca69bb67734cae927c50faeb4daaaacaa61cc852bd1af8f97b65cc50d07"},
		// The last set truncates a product of two earlier ones, so results reach only the first values and prefixes.
		{"truncated product", "{p,q}{" + strings.Repeat("a", 60) + "{1..99999},b}{x,y}", 60033, 3999999, "0a8cd0830e3b01cc2554e555237e9e9f8e6094248e45d75df2bf463a522a531f"},
		{"empty values at EXPANSION_MAX", strings.Repeat("{,}", 15) + "{a,,,}{,}", 50000, 50000, "803cdf9ad3f562e84d55e409a5bfe5ffd645e47e2094b09829ac5574cee7e121"},
		{"empty first value at EXPANSION_MAX", "{,a}{,,{1..99999}}", 100000, 488886, "4b50fe05c32c8a6325fb5e3227ccc82be40ac1109b97ad4b9fded6876c8ac07e"},
		{"wide sequence", "{" + strings.Repeat("0", 5000) + "1..99999}", 799, 3995799, "39c8e4ad63add7c6636f9af45c5463d84357174940e71753785ca6c71c37c2c7"},
		{"chained sets with an empty first value", strings.Repeat("{,{1..99999}}", 50), 100000, 488890, "9d52864599df7d533da933d1d471abf1bed6d7bd2a72efc7d988287b2981d083"},
		// EXPANSION_MAX cuts the product of 40,000 numbers and three letters at 100,000, so the results reach the first ceil(100,000 / 3) = 33,334 numbers: the last is "33334a{x}".
		{"partial parent row", "{1..40000}{a,b,c}{x}", 100000, 866682, "fbe6bc34a2c4dea11585ce0181642e085c02de6e5a644e0bf43341a96c0a9828"},
		// Values and results that fill EXPANSION_MAX_LENGTH exactly are all kept.
		{"values at the length limit", "{{" + strings.Repeat("0", 49) + "1..99999},b}", 80000, 4000000, "3a0eb81ed7233b3fdc77845c6bbe426f7f75a1435175ab0b1a495a75269b7522"},
		{"results at the length limit", "x{" + strings.Repeat("0", 48) + "1..99999}", 80000, 4000000, "550d01396b7520fb9089183abd03d2f48185b144b53c9aa53aca8416f6f21043"},
		{"stuck sequence", "{9007199254740992..9007199254741000}", 100000, 1600000, "802917996bb05b18d459b322b5218cb4c7ea263fb9ecad58ac58d7a3799b5d8b"},
		{"long sequence", "x{1..200000}", 100000, 588895, "4f5b60aaf05462b3a0e05dd9fc2aa91c28b9663ac0b334894e93efd8f71f2722"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := npmBraceExpand(tc.pattern)
			total := 0
			for _, s := range got {
				total += jsstring.Length(s)
			}
			sum := sha256.Sum256([]byte(strings.Join(got, "\n")))
			if len(got) != tc.count || total != tc.total || hex.EncodeToString(sum[:]) != tc.sha {
				t.Fatalf("%d results, %d units, sha %x; want %d, %d, %s", len(got), total, sum, tc.count, tc.total, tc.sha)
			}
		})
	}
}

// Pi's minimatch builds its set from brace-expansion 5.0.12, so a filter whose sequence Node's bundled copy cannot hold still compiles and matches. Probed with Pi 1.1.0 (r25): {"prompts":["!x{1..134217729}"]} starts and lists the prompt, and minimatch("x5", "x{1..134217729}") is true.
func TestMinimatchFilterExpandsBracesAsPi(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"x{1..134217729}", "x5", true},
		{"x{1..134217729}", "x100001", false},
		{"{a.md,}{},,}", "a.md", true},
		{"{a.md,}{},,}", "a.md}", true},
		{"x{9007199254740993..9007199254740995}", "x9007199254740992", true},
	} {
		m, err := newMinimatchFilter(tc.pattern)
		if err != nil {
			t.Fatalf("newMinimatchFilter(%q): %v", tc.pattern, err)
		}
		if got := m.match(tc.path); got != tc.want {
			t.Errorf("minimatch(%q, %q) = %t, want %t", tc.path, tc.pattern, got, tc.want)
		}
	}
}

// The product keeps only the values a kept result reaches. Each chained set "{,{1..99999}}" gives 100,000 values, and EXPANSION_MAX keeps only the results that extend the first result before, so every earlier level keeps one value, and the dropped values can be freed: the product's memory stays bounded however many sets there are.
func TestBraceProductKeepsOnlyReachableValues(t *testing.T) {
	values := func() braceValues {
		v := braceValues{}
		v.add("", 0)
		for i := 1; i < 100000; i++ {
			s := strconv.Itoa(i)
			v.add(s, len(s))
		}
		return v
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	p := newBraceProduct()
	for range 20 {
		p.extend("", values())
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	kept := 0
	for _, level := range p.levels {
		kept += len(level.values)
	}
	if want := 100000 + len(p.levels) - 1; p.count != braceExpansionMax || kept != want {
		t.Fatalf("%d results, %d values kept over %d levels; want %d results and %d values", p.count, kept, len(p.levels), braceExpansionMax, want)
	}
	if got := p.textOf(braceExpansionMax-1, make([]int, len(p.levels))); got != "99999" {
		t.Fatalf("last result %q, want 99999", got)
	}
	// One level's values and the results' lengths take about 5 MB; twenty levels' would take about 70 MB.
	if retained := (int64(after.HeapAlloc) - int64(before.HeapAlloc)) >> 20; retained > 24 {
		t.Fatalf("the product retains %d MB", retained)
	}
	runtime.KeepAlive(p)
}

// A level whose values all stay reachable can still have fewer reachable results, so pruning goes on below it: after 100,000 numbers, {a,b} and 10,000 names, the names reach 10 results of {a,b}, and those reach 5 numbers.
func TestBraceProductPrunesBelowAFullyReachedLevel(t *testing.T) {
	set := func(prefix string, n int, empty bool) braceValues {
		v := braceValues{}
		if empty {
			v.add("", 0)
		}
		for i := len(v.items); i < n; i++ {
			s := prefix + strconv.Itoa(i)
			v.add(s, len(s))
		}
		return v
	}
	p := newBraceProduct()
	p.extend("", set("", 100000, true))
	p.extend("", braceValues{items: []string{"a", "b"}, units: []int{1, 1}})
	p.extend("", set("x", 10000, false))
	var kept []int
	for _, level := range p.levels {
		kept = append(kept, len(level.values))
	}
	if !slices.Equal(kept, []int{5, 2, 10000}) || p.count != braceExpansionMax {
		t.Fatalf("values kept per level %v, %d results; want [5 2 10000] and %d", kept, p.count, braceExpansionMax)
	}
	if got := p.textOf(braceExpansionMax-1, make([]int, 3)); got != "4bx9999" {
		t.Fatalf("last result %q, want 4bx9999", got)
	}
}

// The limits bound memory, as V8's string ropes do: a sequence stops at EXPANSION_MAX_LENGTH before it builds more text, and parseCommaParts builds a long run of sets in one buffer.
func TestNpmBraceExpandBoundsAllocation(t *testing.T) {
	for _, tc := range []struct {
		name, pattern string
		maxMB         uint64
	}{
		{"wide sequence", "{" + strings.Repeat("0", 5000) + "1..99999}", 64},
		{"a run of sets in a comma set", "{" + strings.Repeat("{a}", 16000) + ",x}", 16},
	} {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		npmBraceExpand(tc.pattern)
		runtime.ReadMemStats(&after)
		if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb > tc.maxMB {
			t.Errorf("%s: allocated %d MB, want at most %d", tc.name, mb, tc.maxMB)
		}
	}
}

// A combine that keeps no result leaves nothing to extend: later sets keep none either, and the last combine gives no result.
func TestBraceProductWithNoResult(t *testing.T) {
	p := newBraceProduct()
	p.extend("x", braceValues{})
	p.extend("", braceValues{items: []string{"a", "b"}, units: []int{1, 1}})
	if p.count != 0 {
		t.Fatalf("%d results after an empty set, want 0", p.count)
	}
	if got := p.finish("y", literalBraceValue("z"), false); len(got.items) != 0 {
		t.Fatalf("finish = %q, want no result", got.items)
	}
}
