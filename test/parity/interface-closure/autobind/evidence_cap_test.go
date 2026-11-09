package main

import (
	"slices"
	"testing"
)

// Config.name is given a declaration file of its own, so that the other Config members (whose TestConfig does run lib.go) do not stand in.
func nameInItsOwnFile(inv []m) []m {
	for _, e := range inv {
		if e["id"] == "pkg:fx/.#Config::property:name" {
			e["source"] = m{"path": "dist/name.d.ts", "line": 1}
		}
	}
	return inv
}

// A row lists at most three tests, best first. When no row of a Pi declaration file lists a test that runs the Go file the rows map
// to, and a lower-ranked asserting test does, the cap must not drop that test: portmap-check asks the evidence tests of a Pi file to
// execute a statement of the Go file its members map to, and a new, higher-ranked test (a conformance test that only builds a value)
// used to push the one running test out. Red without keepFileRunners: the evidence is the three conformance tests, and TestConfig, the
// only test that runs lib.go, is dropped.
func TestEvidenceCapKeepsATestThatRunsTheMappedFile(t *testing.T) {
	const conformance = `package lib

import "testing"

func TestConformanceNameA(t *testing.T) {
	if (Config{Name: "a"}).Name != "a" {
		t.Error("name")
	}
}

func TestConformanceNameB(t *testing.T) {
	if (Config{Name: "b"}).Name != "b" {
		t.Error("name")
	}
}

func TestConformanceNameC(t *testing.T) {
	if (Config{Name: "c"}).Name != "c" {
		t.Error("name")
	}
}
`
	// A file name that holds "conformance" ranks its tests above TestConfig, which names Config.Name too and calls Config.Run.
	ds := fixtureRun(t, map[string]string{"lib/name_conformance_test.go": conformance}, nameInItsOwnFile, nil, nil)
	d := ds["pkg:fx/.#Config::property:name"]
	if d == nil || d.Gap || d.Info == nil {
		t.Fatalf("Config.name decision = %+v", d)
	}
	want := []string{
		"test:lib/name_conformance_test.go#TestConformanceNameA",
		"test:lib/name_conformance_test.go#TestConformanceNameB",
		"test:lib/lib_test.go#TestConfig",
	}
	if !slices.Equal(d.Info.Tests, want) {
		t.Fatalf("Config.name evidence = %v, want %v", d.Info.Tests, want)
	}
}

// When one of the three best tests already runs the mapped file the cap changes nothing.
func TestEvidenceCapLeavesTheBestThreeWhenTheyRunTheFile(t *testing.T) {
	const conformance = `package lib

import "testing"

func TestConformanceNameA(t *testing.T) {
	if Add(1, 2) != 3 || (Config{Name: "a"}).Name != "a" {
		t.Error("name")
	}
}

func TestConformanceNameB(t *testing.T) {
	if (Config{Name: "b"}).Name != "b" {
		t.Error("name")
	}
}

func TestConformanceNameC(t *testing.T) {
	if (Config{Name: "c"}).Name != "c" {
		t.Error("name")
	}
}
`
	ds := fixtureRun(t, map[string]string{"lib/name_conformance_test.go": conformance}, nameInItsOwnFile, nil, nil)
	d := ds["pkg:fx/.#Config::property:name"]
	if d == nil || d.Gap || d.Info == nil {
		t.Fatalf("Config.name decision = %+v", d)
	}
	want := []string{
		"test:lib/name_conformance_test.go#TestConformanceNameA",
		"test:lib/name_conformance_test.go#TestConformanceNameB",
		"test:lib/name_conformance_test.go#TestConformanceNameC",
	}
	if !slices.Equal(d.Info.Tests, want) {
		t.Fatalf("Config.name evidence = %v, want %v", d.Info.Tests, want)
	}
}
