package host

import "testing"

// Renew is the Go form of evaluating a plugin's module again: a new facet of the same setup, not the same one.
func TestRenewMakesANewFacetOfTheSameSetup(t *testing.T) {
	facet := DefineFacet(func(FacetHost, FacetSelf) error { return nil })
	renewed := facet.Renew()
	if renewed.same(facet) {
		t.Fatal("a renewed facet is the same facet as its original")
	}
	if !renewed.hasSetup() || renewed.definition.setup == nil {
		t.Fatal("a renewed facet has no setup")
	}
	if !renewed.same(renewed) {
		t.Fatal("a facet is not the same as itself")
	}
}
