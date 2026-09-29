// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package lazyregexp

import (
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func compiled(r *Regexp) bool { return r.compiled.Load() }

// New must not compile: that is the start-up work the package exists to defer.
func TestNewDoesNotCompileUntilFirstUse(t *testing.T) {
	r := New(`a(b+)c`)
	if compiled(r) {
		t.Fatal("New compiled the pattern")
	}
	if got := r.String(); got != `a(b+)c` {
		t.Fatalf("String() = %q", got)
	}
	if compiled(r) {
		t.Fatal("String compiled the pattern")
	}
	if !r.MatchString("xabbcx") {
		t.Fatal("MatchString missed a match")
	}
	if !compiled(r) {
		t.Fatal("first use did not compile")
	}
}

// Every method returns what the wrapped *regexp.Regexp returns.
func TestMethodsMatchRegexp(t *testing.T) {
	const pattern = `(?P<word>[a-z]+)(\d*)`
	const text = "foo12 bar baz7"
	want, got := regexp.MustCompile(pattern), New(pattern)
	checks := []struct {
		name      string
		want, got any
	}{
		{"MatchString", want.MatchString(text), got.MatchString(text)},
		{"FindString", want.FindString(text), got.FindString(text)},
		{"FindStringIndex", want.FindStringIndex(text), got.FindStringIndex(text)},
		{"FindStringSubmatch", want.FindStringSubmatch(text), got.FindStringSubmatch(text)},
		{"FindStringSubmatchIndex", want.FindStringSubmatchIndex(text), got.FindStringSubmatchIndex(text)},
		{"FindAllString", want.FindAllString(text, -1), got.FindAllString(text, -1)},
		{"FindAllStringSubmatch", want.FindAllStringSubmatch(text, 2), got.FindAllStringSubmatch(text, 2)},
		{"FindAllStringIndex", want.FindAllStringIndex(text, -1), got.FindAllStringIndex(text, -1)},
		{"ReplaceAllString", want.ReplaceAllString(text, "<${word}>"), got.ReplaceAllString(text, "<${word}>")},
		{"ReplaceAllLiteralString", want.ReplaceAllLiteralString(text, "$1"), got.ReplaceAllLiteralString(text, "$1")},
		{"ReplaceAllStringFunc", want.ReplaceAllStringFunc(text, strings.ToUpper), got.ReplaceAllStringFunc(text, strings.ToUpper)},
		{"Split", want.Split(text, -1), got.Split(text, -1)},
		{"NumSubexp", want.NumSubexp(), got.NumSubexp()},
		{"SubexpNames", want.SubexpNames(), got.SubexpNames()},
		{"SubexpIndex", want.SubexpIndex("word"), got.SubexpIndex("word")},
		{"Find", want.Find([]byte(text)), got.Find([]byte(text))},
		{"FindSubmatch", want.FindSubmatch([]byte(text)), got.FindSubmatch([]byte(text))},
		{"ReplaceAll", want.ReplaceAll([]byte(text), []byte("_")), got.ReplaceAll([]byte(text), []byte("_"))},
		{"Match", want.Match([]byte(text)), got.Match([]byte(text))},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.want, check.got) {
			t.Errorf("%s = %#v, want %#v", check.name, check.got, check.want)
		}
	}
	if got.Regexp().String() != pattern {
		t.Errorf("Regexp() compiled %q", got.Regexp().String())
	}
}

// An invalid pattern panics with regexp.MustCompile's message, on first use instead of at init.
func TestInvalidPatternPanicsLikeMustCompileOnFirstUse(t *testing.T) {
	// Built at run time so staticcheck does not parse the deliberately invalid pattern.
	pattern := "a(" + strings.TrimSpace(" b")
	var want any
	func() {
		defer func() { want = recover() }()
		regexp.MustCompile(pattern)
	}()
	r := New(pattern)
	var got any
	func() {
		defer func() { got = recover() }()
		r.MatchString("ab")
	}()
	if want == nil || got != want {
		t.Fatalf("panic = %v, want %v", got, want)
	}
	if invalid := CompileAll(); !strings.Contains(invalid, pattern) {
		t.Fatalf("CompileAll() = %q, want it to name %q", invalid, pattern)
	}
}

func TestConcurrentFirstUseCompilesOnce(t *testing.T) {
	r := New(`x+`)
	var wg sync.WaitGroup
	results := make([]bool, 64)
	for i := range results {
		wg.Go(func() { results[i] = r.MatchString("xxx") })
	}
	wg.Wait()
	for i, ok := range results {
		if !ok {
			t.Fatalf("goroutine %d saw no match", i)
		}
	}
}
