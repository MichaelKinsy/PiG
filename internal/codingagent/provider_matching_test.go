package codingagent

// pi: packages/coding-agent/src/core/provider-matching.ts

// Ports packages/coding-agent/test/provider-matching.test.ts

import (
	"slices"
	"testing"
)

type matchingProvider struct{ id, name, authType string }

func matchingIDName(p *matchingProvider) (string, string) { return p.id, p.name }

func TestProviderReferenceMatching(t *testing.T) {
	providers := []*matchingProvider{
		{"first", "Shared Name", "oauth"},
		{"first", "Shared Name", "api_key"},
		{"other", "FIRST", "api_key"},
		{"second", "Second Provider", "oauth"},
	}
	match := func(ref string) []*matchingProvider { return FindMatchingProviders(providers, ref, matchingIDName) }

	t.Run("matches IDs and display names case-insensitively with surrounding whitespace", func(t *testing.T) {
		for _, ref := range []string{"  sEcOnD  ", "\tSECOND PROVIDER\n"} {
			if got := match(ref); !slices.Equal(got, providers[3:4]) {
				t.Fatalf("match(%q) = %v", ref, got)
			}
		}
	})
	t.Run("keeps all matching providers and auth methods in input order", func(t *testing.T) {
		matched := match("first")
		if !slices.Equal(matched, providers[:3]) || matched[0] != providers[0] || matched[1].authType != "api_key" {
			t.Fatalf("match(first) = %v", matched)
		}
		if got := match("shared name"); !slices.Equal(got, providers[:2]) {
			t.Fatalf("match(shared name) = %v", got)
		}
	})
	t.Run("does not fuzzy-match unknown or partial references", func(t *testing.T) {
		if len(match("unknown")) != 0 || len(match("sec")) != 0 || len(FindMatchingProviders(nil, "first", matchingIDName)) != 0 {
			t.Fatal("matched an unknown or partial reference")
		}
	})
	t.Run("never matches an empty reference, even against empty display names", func(t *testing.T) {
		if got := FindMatchingProviders([]*matchingProvider{{id: "unnamed"}}, "", matchingIDName); len(got) != 0 {
			t.Fatalf("empty reference matched %v", got)
		}
		if got := match(" \t "); len(got) != 0 {
			t.Fatalf("blank reference matched %v", got)
		}
	})
}
