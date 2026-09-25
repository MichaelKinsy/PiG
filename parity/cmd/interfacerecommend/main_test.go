package main

import "testing"

func TestRecommendationsNeverPromoteNameMatchesToPorted(t *testing.T) {
	upstream := semanticInventory{UpstreamVersion: "0.83.0", Interfaces: []semanticEntry{
		{ID: "pkg:coding-agent/.#ExtensionAPI", Name: "ExtensionAPI", ShapeHash: "sha256:upstream"},
		{ID: "pkg:coding-agent/.#ExtensionAPI::property:on", ParentID: "pkg:coding-agent/.#ExtensionAPI", Name: "ExtensionAPI.on", ShapeHash: "sha256:member"},
		{ID: "pkg:coding-agent/.#Missing", Name: "Missing", ShapeHash: "sha256:missing"},
		{ID: "cli:pi/--approve", Flag: "--approve", ShapeHash: "sha256:cli"},
	}}
	pig := goInventory{Interfaces: []goEntry{{ID: "go:example#ExtensionAPI", Name: "ExtensionAPI", ShapeHash: "sha256:pig"}}}
	ledger := recommend(upstream, pig)
	if got := ledger.Recommendations[1]; got.RecommendedDisposition != "partial" || got.Confidence != "low" {
		t.Fatalf("exact candidate recommendation = %+v", got)
	}
	if got := ledger.Recommendations[2]; got.RecommendedDisposition != "pending" || got.Confidence != "low" || len(got.PigCandidates) != 0 {
		t.Fatalf("member recommendation = %+v", got)
	}
	for _, item := range ledger.Recommendations {
		if item.RecommendedDisposition == "ported" {
			t.Fatalf("recommendation promoted %s to ported", item.ID)
		}
	}
}

func TestNormalizedNameHandlesGoMethodsAndInitialisms(t *testing.T) {
	if normalizedName("Context.GetAPIKey") != normalizedName("getApiKey") {
		t.Fatalf("normalized names differ")
	}
}
