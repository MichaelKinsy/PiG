// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package lazyregexp compiles a package-level regular expression when it is first used instead of when its package initializes.
//
// Process start pays for every package-level regexp.MustCompile in every linked package, whether or not the run reaches the code that uses it. A Regexp from New exposes the *regexp.Regexp methods its callers use, each returning what the wrapped *regexp.Regexp returns, and Regexp returns the wrapped value for any other use. It compiles the same pattern with regexp.MustCompile, panics with the same message for an invalid pattern, and is safe for concurrent use. Only the moment of compilation and of that panic moves to the first method call. CompileAll compiles every pattern so a test can prove each one is valid.
package lazyregexp

import (
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"
)

// Regexp is a lazily compiled *regexp.Regexp.
type Regexp struct {
	expr     string
	compile  func() *regexp.Regexp
	compiled atomic.Bool
}

var (
	mu       sync.Mutex
	patterns []*Regexp
)

// New returns a Regexp for expr. It does not compile expr.
func New(expr string) *Regexp {
	r := &Regexp{expr: expr}
	// OnceValue repeats the panic of an invalid pattern on every use, as a package that failed to initialize would have stopped the process.
	r.compile = sync.OnceValue(func() *regexp.Regexp {
		r.compiled.Store(true)
		return regexp.MustCompile(expr)
	})
	mu.Lock()
	patterns = append(patterns, r)
	mu.Unlock()
	return r
}

// CompileAll compiles every pattern created so far and returns the first panic message of an invalid pattern, or "".
func CompileAll() (invalid string) {
	mu.Lock()
	all := append([]*Regexp(nil), patterns...)
	mu.Unlock()
	for _, r := range all {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil && invalid == "" {
					invalid = fmt.Sprint(recovered)
				}
			}()
			r.get()
		}()
	}
	return invalid
}

func (r *Regexp) get() *regexp.Regexp { return r.compile() }

// Regexp returns the compiled expression.
func (r *Regexp) Regexp() *regexp.Regexp { return r.get() }

func (r *Regexp) String() string { return r.expr }

func (r *Regexp) NumSubexp() int { return r.get().NumSubexp() }

func (r *Regexp) SubexpNames() []string { return r.get().SubexpNames() }

func (r *Regexp) SubexpIndex(name string) int { return r.get().SubexpIndex(name) }

func (r *Regexp) Match(b []byte) bool { return r.get().Match(b) }

func (r *Regexp) MatchString(s string) bool { return r.get().MatchString(s) }

func (r *Regexp) Find(b []byte) []byte { return r.get().Find(b) }

func (r *Regexp) FindString(s string) string { return r.get().FindString(s) }

func (r *Regexp) FindStringIndex(s string) []int { return r.get().FindStringIndex(s) }

func (r *Regexp) FindSubmatch(b []byte) [][]byte { return r.get().FindSubmatch(b) }

func (r *Regexp) FindStringSubmatch(s string) []string { return r.get().FindStringSubmatch(s) }

func (r *Regexp) FindStringSubmatchIndex(s string) []int { return r.get().FindStringSubmatchIndex(s) }

func (r *Regexp) FindAllString(s string, n int) []string { return r.get().FindAllString(s, n) }

func (r *Regexp) FindAllStringIndex(s string, n int) [][]int { return r.get().FindAllStringIndex(s, n) }

func (r *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	return r.get().FindAllStringSubmatch(s, n)
}

func (r *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	return r.get().FindAllStringSubmatchIndex(s, n)
}

func (r *Regexp) ReplaceAll(src, repl []byte) []byte { return r.get().ReplaceAll(src, repl) }

func (r *Regexp) ReplaceAllString(src, repl string) string {
	return r.get().ReplaceAllString(src, repl)
}

func (r *Regexp) ReplaceAllStringFunc(src string, repl func(string) string) string {
	return r.get().ReplaceAllStringFunc(src, repl)
}

func (r *Regexp) ReplaceAllLiteralString(src, repl string) string {
	return r.get().ReplaceAllLiteralString(src, repl)
}

func (r *Regexp) Split(s string, n int) []string { return r.get().Split(s, n) }
