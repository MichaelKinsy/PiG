// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// upstreamIndex resolves `// upstream: <file>:<symbol>` references against
// the pinned Pi mirror (.upstream/current).
type upstreamIndex struct {
	root  string
	files []string // slash paths relative to root
	cache map[string]string
}

func loadUpstreamIndex(root string) (*upstreamIndex, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("upstream mirror %s is missing; run `make upstream-mirror`", root)
	}
	// .upstream/current is a symlink to the pinned version; walk its target.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &upstreamIndex{root: root, cache: map[string]string{}}, nil
}

// list walks the mirror once, on the first reference that needs resolving.
func (u *upstreamIndex) list() ([]string, error) {
	if u.files != nil {
		return u.files, nil
	}
	u.files = []string{}
	err := filepath.WalkDir(u.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(u.root, path)
		if err != nil {
			return err
		}
		u.files = append(u.files, filepath.ToSlash(rel))
		return nil
	})
	return u.files, err
}

// resolve finds ref as a path under packages/, under the mirror root, or as
// the unique mirror file whose path ends with /ref.
func (u *upstreamIndex) resolve(ref string) (string, error) {
	ref = strings.TrimPrefix(ref, "./")
	files, err := u.list()
	if err != nil {
		return "", err
	}
	var suffix []string
	for _, f := range files {
		if f == "packages/"+ref || f == ref {
			return f, nil
		}
		if strings.HasSuffix(f, "/"+ref) {
			suffix = append(suffix, f)
		}
	}
	switch len(suffix) {
	case 0:
		return "", fmt.Errorf("no file %q in the upstream mirror", ref)
	case 1:
		return suffix[0], nil
	}
	return "", fmt.Errorf("%q matches %d upstream files; give more of the path", ref, len(suffix))
}

func (u *upstreamIndex) content(rel string) (string, error) {
	if s, ok := u.cache[rel]; ok {
		return s, nil
	}
	data, err := os.ReadFile(filepath.Join(u.root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	u.cache[rel] = string(data)
	return u.cache[rel], nil
}

// upstreamRefRe matches `upstream: <file>[:<symbol or line range>]`.
var upstreamRefRe = regexp.MustCompile(`upstream: ([\w@./-]+\.(?:ts|tsx|js|mjs|cjs|json))(?::([A-Za-z_$][\w$.]*|[0-9]+(?:-[0-9]+)?))?`)

// verifyRef checks one upstream reference. It returns an error when the file
// does not exist in the mirror or a named symbol does not appear in it, and
// otherwise reports whether one of the value spellings appears in the file.
func (u *upstreamIndex) verifyRef(file, symbol string, values []*regexp.Regexp) (bool, error) {
	rel, err := u.resolve(file)
	if err != nil {
		return false, err
	}
	text, err := u.content(rel)
	if err != nil {
		return false, err
	}
	if symbol != "" && !isLineRange(symbol) {
		last := symbol[strings.LastIndex(symbol, ".")+1:]
		if !strings.Contains(text, last) {
			return false, fmt.Errorf("symbol %q does not appear in %s", symbol, rel)
		}
	}
	for _, re := range values {
		if re.MatchString(text) {
			return true, nil
		}
	}
	return false, nil
}

func isLineRange(s string) bool {
	return s != "" && strings.Trim(s, "0123456789-") == ""
}

// jsNumberRe matches v written as a JavaScript numeric literal, with or
// without `_` digit separators.
func jsNumberRe(v *big.Int) *regexp.Regexp {
	digits := v.String()
	var b strings.Builder
	b.WriteString(`(?:^|[^\w.])`)
	for i, r := range digits {
		if i > 0 {
			b.WriteString(`_?`)
		}
		b.WriteRune(r)
	}
	b.WriteString(`(?:[^\w]|$)`)
	return regexp.MustCompile(b.String())
}

// mappedValue reports whether one of values appears in one of the upstream
// files (mirror-relative paths from PORT_MAP.md).
func (u *upstreamIndex) mappedValue(files []string, values []*regexp.Regexp) bool {
	for _, f := range files {
		text, err := u.content(f)
		if err != nil {
			continue
		}
		for _, re := range values {
			if re.MatchString(text) {
				return true
			}
		}
	}
	return false
}

// spellings lists the patterns that find a literal's value in upstream
// source: each numeric value as a JavaScript literal, and, for a plain
// constant expression such as `64 * 1024`, the same expression.
func spellings(values []*big.Int, expr string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, v := range values {
		out = append(out, jsNumberRe(v))
	}
	if expr != "" {
		out = append(out, exprSpellingRe(expr))
	}
	return out
}

var exprTokenRe = regexp.MustCompile(`[0-9][0-9_.xXa-fA-F]*|<<|[-+*/()]`)

// exprSpellingRe matches a constant expression token by token, ignoring
// whitespace and digit separators.
func exprSpellingRe(expr string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`(?:^|[^\w.])`)
	for i, tok := range exprTokenRe.FindAllString(expr, -1) {
		if i > 0 {
			b.WriteString(`\s*`)
		}
		if tok[0] >= '0' && tok[0] <= '9' {
			digits := strings.ReplaceAll(tok, "_", "")
			for j, r := range digits {
				if j > 0 {
					b.WriteString(`_?`)
				}
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
			continue
		}
		b.WriteString(regexp.QuoteMeta(tok))
	}
	b.WriteString(`(?:[^\w]|$)`)
	return regexp.MustCompile(b.String())
}
