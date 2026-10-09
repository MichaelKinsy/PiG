package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestErrorUnionPinned (S5s): a `boolean | string` result is the Go error only when the Go declaration's doc comment states all three
// outcomes and names a test of the package that exercises all three. Every missing piece is a refusal.
func TestErrorUnionPinned(t *testing.T) {
	const doc = `	// Copy writes text. A nil error is true, a non-empty error message is that string, and an empty error message is false.
	// TestCopyOutcomes pins all three.
`
	const goodTest = `package pkg

import (
	"errors"
	"testing"
)

func TestCopyOutcomes(t *testing.T) {
	for _, err := range []error{nil, errors.New("clipboard unavailable"), errors.New("")} {
		_ = err
	}
}
`
	for _, tc := range []struct {
		name     string
		docLine  string
		testBody string
		upstream string
		want     bool
	}{
		{"documented and pinned", doc, goodTest, "Promise<boolean | string>", true},
		{"result order does not matter", doc, goodTest, "string | boolean", true},
		{"a plain boolean | string result", doc, goodTest, "boolean | string", true},
		{"no doc comment", "", goodTest, "Promise<boolean | string>", false},
		{"doc omits the empty-message case", strings.Replace(doc, ", and an empty error message is false", "", 1), goodTest, "Promise<boolean | string>", false},
		{"doc omits the nil case", strings.Replace(doc, "A nil error is true, ", "", 1), goodTest, "Promise<boolean | string>", false},
		{"doc omits the message case", strings.Replace(doc, "a non-empty error message is that string, ", "", 1), goodTest, "Promise<boolean | string>", false},
		{"doc names no test", strings.Replace(doc, "TestCopyOutcomes pins all three.", "A test pins all three.", 1), goodTest, "Promise<boolean | string>", false},
		{"the named test does not exist", doc, strings.Replace(goodTest, "TestCopyOutcomes", "TestOther", 1), "Promise<boolean | string>", false},
		{"the test lacks the nil case", doc, strings.Replace(goodTest, "nil, errors", "errors", 1), "Promise<boolean | string>", false},
		{"the test lacks the empty-message case", doc, strings.Replace(goodTest, `, errors.New("")`, "", 1), "Promise<boolean | string>", false},
		{"the test lacks the message case", doc, strings.Replace(goodTest, `errors.New("clipboard unavailable"), `, "", 1), "Promise<boolean | string>", false},
		{"another union result", doc, goodTest, "number | string", false},
		{"a boolean result", doc, goodTest, "boolean", false},
	} {
		dir := t.TempDir()
		src := "package pkg\n\ntype Options struct {\n" + tc.docLine + "\tCopy func(text string) error\n}\n"
		if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{"pkg/options.go": src, "pkg/options_test.go": tc.testBody} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		d := &detector{ix: &index{root: dir}}
		s := &sym{Kind: "field", Name: "Copy", Owner: "Options", Dir: "pkg", File: "pkg/options.go"}
		if got := d.errorUnionPinned(tc.upstream, s); got != tc.want {
			t.Errorf("%s: pinned = %v, want %v", tc.name, got, tc.want)
		}
	}
}
