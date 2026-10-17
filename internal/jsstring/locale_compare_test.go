package jsstring

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
)

// `localeCompare` of Node orders these names; a byte or ASCII-weight comparison does not (hyphen, underscore and dot,
// accents, digits and case).
func TestLocaleCompareOrdersAsNodeDoes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("the Node oracle is required: %v", err)
	}
	names := []string{"my.server", "my-server", "my_server", "myserver", "My Server", "my server", "über", "uber", "zeta", "Alpha", "alpha", "a1", "a10", "a2", "café", "cafe", "x:y", "x@y", "x~y", "x/y"}
	input, _ := json.Marshal(names)
	out, err := exec.Command(node, "-e", `const n=JSON.parse(process.argv[1]);console.log(JSON.stringify([...n].sort((a,b)=>a.localeCompare(b))))`, string(input)).Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	got := slices.Clone(names)
	slices.SortStableFunc(got, LocaleCompare)
	if !slices.Equal(got, want) {
		t.Errorf("order = %q\nNode  = %q", got, want)
	}
}

// Concurrent sorts share no collator state; run with -race.
func TestLocaleCompareIsSafeForConcurrentUse(t *testing.T) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				if LocaleCompare("Sessión-1", "Sessión-2") >= 0 || LocaleCompare("b", "a") <= 0 || LocaleCompare("a", "a") != 0 {
					t.Error("unexpected order")
					return
				}
			}
		})
	}
	wg.Wait()
}

// ICU weighs U+FFFD after every code point but U+FFFF; the x/text tables weigh it like a symbol between "-" and "1". Every pair of these strings, with U+FFFD alone, inside and at the ends of a name, compares as Node's does.
func TestLocaleComparePlacesReplacementCharacterAsNodeDoes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("the Node oracle is required: %v", err)
	}
	names := []string{"", "\uFFFD", "\uFFFD\uFFFD", "\uFFFDx", "x\uFFFD", "x\uFFFDy", "xy", "x", "a\uFFFDb", "ab", "aé", "a-b", "a1", "1", "-", " ", "Z", "z", "\u4e00", "\u4e00\uFFFD", "\U0010FFFE", "\ue000", "\u0378", "\uFFFC", "\uFFFE", "e\u0301\uFFFD", "\u00e9\uFFFD", "bad\uFFFDname.txt", "bad\uFFFD", "dir\uFFFD/", "z\uFFFD\uFFFD\uFFFD", "\uFFFF", "\uFFFF\uFFFF", "a\uFFFF", "\uFFFD\uFFFF", "\uFFFF\uFFFD", "\uFFFF\t", "\uFFFD\t", "\t", "a\uFFFDb\uFFFF", "a\uFFFFb\uFFFD", "a\uFFFFb\uFFFF", "\uFFFD\u0301", "\uFFFF\u0301", "\uFFFDa", "\uFFFFa"}
	input, _ := json.Marshal(names)
	out, err := exec.Command(node, "-e", `const n=JSON.parse(process.argv[1]);console.log(JSON.stringify(n.map(a=>n.map(b=>a.localeCompare(b)))))`, string(input)).Output()
	if err != nil {
		t.Fatal(err)
	}
	var want [][]int
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, a := range names {
		for j, b := range names {
			if got := LocaleCompare(a, b); got != want[i][j] {
				t.Errorf("LocaleCompare(%q, %q) = %d, Node %d", a, b, got, want[i][j])
			}
		}
	}
}

// Random strings built from U+FFFD, U+FFFF, U+FFFE, unassigned and private-use code points, case, accents, punctuation, spaces and CJK compare as Node's localeCompare does, including the tie
// between U+FFFD and a literal U+FFFF (ICU sorts U+FFFD before U+FFFF). U+10FFFF and Tangut are left out: x/text weighs them differently from ICU with or without U+FFFD (its tables are older).
func TestLocaleCompareRandomStringsWithNoncharactersMatchNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("the Node oracle is required: %v", err)
	}
	pieces := []string{"\uFFFD", "\uFFFF", "\uFFFE", "a", "A", "b", "-", " ", "1", "\t", "\u0301", "é", "z", "中", "\u0378", "\ue000", "\U0003FFFF", "\U0010FFFE", "_", ""}
	rng := rand.New(rand.NewSource(7))
	random := func() string {
		var b strings.Builder
		for range rng.Intn(5) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		return b.String()
	}
	pairs := make([][2]string, 20000)
	for i := range pairs {
		pairs[i] = [2]string{random(), random()}
	}
	input, err := json.Marshal(pairs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", `const p=JSON.parse(require("fs").readFileSync(0,"utf8"));console.log(JSON.stringify(p.map(([a,b])=>a.localeCompare(b))))`)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []int
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, pair := range pairs {
		if got := LocaleCompare(pair[0], pair[1]); got != want[i] {
			t.Errorf("LocaleCompare(%q, %q) = %d, Node %d", pair[0], pair[1], got, want[i])
		}
	}
}
