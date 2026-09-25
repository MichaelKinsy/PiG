// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path"
	"regexp"
	"strings"
)

var (
	portMapRowRe = regexp.MustCompile("^\\| `(packages/[^`]+)` \\| `([^`]*)`")
	portMapGoRe  = regexp.MustCompile(`[\w./-]*(?:\{[\w,]+\})?\.go\b`)
)

// loadPortMap maps each Go file named in PORT_MAP.md to the upstream files
// (relative to the mirror root) it ports. A bare file name inherits the
// directory of the path before it in the same cell, and `dir/{a,b}.go`
// expands to both files.
func loadPortMap(root string) (map[string][]string, error) {
	data, err := os.ReadFile(path.Join(root, "PORT_MAP.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]string{}, nil
		}
		return nil, err
	}
	out := map[string][]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		m := portMapRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		dir := ""
		for _, tok := range portMapGoRe.FindAllString(m[2], -1) {
			for _, f := range expandBraces(tok) {
				if strings.Contains(f, "/") {
					dir = path.Dir(f)
				} else if dir != "" {
					f = dir + "/" + f
				}
				out[f] = append(out[f], m[1])
			}
		}
	}
	return out, nil
}

func expandBraces(tok string) []string {
	open, closing := strings.Index(tok, "{"), strings.Index(tok, "}")
	if open < 0 || closing < open {
		return []string{tok}
	}
	var out []string
	for part := range strings.SplitSeq(tok[open+1:closing], ",") {
		out = append(out, tok[:open]+part+tok[closing+1:])
	}
	return out
}
