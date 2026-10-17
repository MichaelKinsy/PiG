package jsonstringify

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

func objectWithKeys(n int, value func(i int) string) string {
	members := make([]string, n)
	for i := range members {
		members[i] = fmt.Sprintf(`"key%d":%s`, i, value(i))
	}
	return "{" + strings.Join(members, ",") + "}"
}

// largeCanonicalObjects is JSON.stringify output whose objects exceed the fast path's 24-keys-per-object and 96-keys-in-all limits.
func largeCanonicalObjects() []string {
	scalar := func(i int) string { return fmt.Sprint(i) }
	nested := func(i int) string { return objectWithKeys(30, scalar) }
	return []string{
		objectWithKeys(25, scalar),
		objectWithKeys(200, scalar),
		objectWithKeys(40, nested),
		"[" + objectWithKeys(100, scalar) + "," + objectWithKeys(100, scalar) + "]",
	}
}

// JSON.stringify output with large objects is recognized as already canonical (PE-F9): an object of more than 24 keys, or more than 96 keys
// in all, used to take the full rewrite, which decodes and re-encodes every level. A repeated key is still refused at any size.
func TestIsCanonicalAcceptsLargeObjects(t *testing.T) {
	scalar := func(i int) string { return fmt.Sprint(i) }
	for _, text := range largeCanonicalObjects() {
		if !IsCanonical([]byte(text)) {
			t.Fatalf("IsCanonical refused canonical text of %d bytes: %.80s", len(text), text)
		}
		got, err := Canonicalize([]byte(text))
		if err != nil || string(got) != text {
			t.Fatalf("Canonicalize changed canonical text (%v)", err)
		}
	}
	for _, at := range []int{0, 23, 24, 25, 199} {
		text := objectWithKeys(200, scalar)
		text = strings.Replace(text, fmt.Sprintf(`"key%d":`, at), `"key100":`, 1)
		if at == 100 {
			continue
		}
		if IsCanonical([]byte(text)) {
			t.Fatalf("IsCanonical accepted a repeated key (key100 at member %d)", at)
		}
	}
	if IsCanonical([]byte(objectWithKeys(30, scalar)[:len(objectWithKeys(30, scalar))-1] + `,"7":1}`)) {
		t.Fatal("IsCanonical accepted an integer-like key in a large object")
	}
}

// On random large and nested objects whose keys repeat by chance, the scan accepts only text the full rewrite leaves unchanged, and accepts
// every such text that has no repeated key.
func TestIsCanonicalLargeObjectsMatchRewrite(t *testing.T) {
	rng := rand.New(rand.NewPCG(21, 22))
	var object func(depth int) (string, bool)
	object = func(depth int) (string, bool) {
		n := 20 + rng.IntN(60)
		seen := map[string]bool{}
		repeated := false
		members := make([]string, n)
		for i := range members {
			key := fmt.Sprintf("k%d", rng.IntN(400))
			repeated = repeated || seen[key]
			seen[key] = true
			value := fmt.Sprint(rng.IntN(1000))
			if depth < 2 && rng.IntN(10) == 0 {
				var inner bool
				value, inner = object(depth + 1)
				repeated = repeated || inner
			}
			members[i] = fmt.Sprintf("%q:%s", key, value)
		}
		return "{" + strings.Join(members, ",") + "}", repeated
	}
	accepted := 0
	for range 2000 {
		text, repeated := object(0)
		rewritten, err := canonicalize([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		fast := IsCanonical([]byte(text))
		if fast && string(rewritten) != text {
			t.Fatalf("scan accepted text the rewrite changes: %.200s", text)
		}
		if fast == repeated {
			t.Fatalf("scan answered %t for text with repeated keys %t: %.200s", fast, repeated, text)
		}
		if fast {
			accepted++
		}
	}
	if accepted == 0 {
		t.Fatal("no random object without repeated keys")
	}
}
