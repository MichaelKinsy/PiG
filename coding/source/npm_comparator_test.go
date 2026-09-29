package source

import (
	"testing"
)

func TestParseNPMPreservesComparatorSets(t *testing.T) {
	// Pi package-manager.ts:1446-1457,1731-1740 retains the entire nonempty selector, including spaces used by npm comparator sets, hyphen ranges, and unions.
	for _, name := range []string{"example", "@scope/example"} {
		for _, selector := range []string{">=1.0.0 <2.0.0", "1.0.0 - 2.0.0", "^1.0.0 || ^2.0.0", ">=1.0.0\t<2.0.0"} {
			for _, prefix := range []string{"npm:", ""} {
				input := prefix + name + "@" + selector
				t.Run(input, func(t *testing.T) {
					ref, err := Parse(input, Options{Bare: BareNPM})
					if err != nil {
						t.Fatalf("valid npm comparator set rejected: %v", err)
					}
					if ref.Kind != KindNPM || ref.NPMName != name || ref.NPMVer != selector || ref.Raw != input || ref.Locator != name+"@"+selector {
						t.Fatalf("selector or identity changed: %#v", ref)
					}
					identity, err := ref.Identity(t.TempDir())
					if err != nil || identity != "npm:"+name {
						t.Fatalf("identity=%q error=%v", identity, err)
					}
				})
			}
		}
	}
}

func BenchmarkParseNPMComparatorSet(b *testing.B) {
	for b.Loop() {
		if _, err := Parse("npm:@scope/example@>=1.0.0 <2.0.0", Options{Bare: BareReject}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestParseNPMComparatorSetKeepsRegistry(t *testing.T) {
	ref, err := Parse("npm:@scope/example@>=1.0.0 <2.0.0?registry=https%3A%2F%2Fnpm.example.com", Options{Bare: BareReject})
	if err != nil {
		t.Fatal(err)
	}
	if ref.NPMName != "@scope/example" || ref.NPMVer != ">=1.0.0 <2.0.0" || ref.NPMRegistry != "https://npm.example.com" {
		t.Fatalf("ref=%#v", ref)
	}
}

func TestParseNPMRejectsInvalidNamesAndLineTerminators(t *testing.T) {
	for _, input := range []string{"npm:bad name@1.0.0", "npm:@bad scope/example@>=1 <2", "npm:example@>=1.0.0\n<2.0.0", "npm:example@>=1.0.0\r<2.0.0", "npm:example@>=1.0.0\u2028<2.0.0", "npm:example@>=1.0.0\u2029<2.0.0"} {
		t.Run(input, func(t *testing.T) {
			if ref, err := Parse(input, Options{Bare: BareReject}); err == nil {
				t.Fatalf("invalid npm source accepted: %#v", ref)
			}
		})
	}
}
