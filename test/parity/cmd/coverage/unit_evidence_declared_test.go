// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Unit evidence may name a test or a fuzz target; a function with the wrong parameter type or a lower-case suffix is neither.
func TestDeclaredTestsAcceptsTestsAndFuzzTargetsOnly(t *testing.T) {
	dir := t.TempDir()
	source := `package p

import "testing"

func TestA(t *testing.T)            {}
func FuzzB(f *testing.F)            {}
func Testlower(t *testing.T)        {}
func FuzzWrong(t *testing.T)        {}
func TestWrong(f *testing.F)        {}
func TestResult(t *testing.T) error { return nil }
func Fuzzlower(f *testing.F)        {}
`
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := declaredTests(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"TestA": true, "FuzzB": true}
	if len(got) != len(want) {
		t.Fatalf("declared %v, want %v", got, want)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("%s not declared: %v", name, got)
		}
	}
}
