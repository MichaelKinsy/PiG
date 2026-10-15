// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Pin is the committed upstream Pi pin (.upstream/pin).
type Pin struct {
	Ref            string `json:"ref"`
	Commit         string `json:"commit"`
	PackageVersion string `json:"packageVersion"`
	Version        string `json:"version"`
}

// findRoot walks up from dir to the directory that holds go.mod.
func findRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no go.mod above %s", abs)
		}
	}
}

// readPin parses the key=value lines of .upstream/pin.
func readPin(root string) (Pin, error) {
	f, err := os.Open(filepath.Join(root, ".upstream", "pin"))
	if err != nil {
		return Pin{}, err
	}
	defer func() { _ = f.Close() }()
	var p Pin
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "ref":
			p.Ref = strings.TrimSpace(v)
		case "commit":
			p.Commit = strings.TrimSpace(v)
		case "package_version":
			p.PackageVersion = strings.TrimSpace(v)
		case "version":
			p.Version = strings.TrimSpace(v)
		}
	}
	if p.Version == "" {
		return p, fmt.Errorf(".upstream/pin has no version")
	}
	return p, sc.Err()
}

var mappingName = regexp.MustCompile(`^mapping-v(.+)\.json$`)

// ledgerVersions lists every version that has a mapping ledger, oldest first.
func ledgerVersions(root string) []string {
	entries, _ := os.ReadDir(filepath.Join(root, "test", "parity", "interfaces"))
	var vs []string
	for _, e := range entries {
		if m := mappingName.FindStringSubmatch(e.Name()); m != nil {
			vs = append(vs, m[1])
		}
	}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
	return vs
}

// versionLess orders "1.1.0" before "1.1.0+42a3497d" before "1.1.1".
func versionLess(a, b string) bool {
	an, ab, _ := strings.Cut(a, "+")
	bn, bb, _ := strings.Cut(b, "+")
	if an != bn {
		ap, bp := semverParts(an), semverParts(bn)
		for i := range 3 {
			if ap[i] != bp[i] {
				return ap[i] < bp[i]
			}
		}
		return an < bn
	}
	return ab < bb
}

func semverParts(v string) [3]int {
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}

// pinTargets picks the ledgers the report shows: the committed pin first, then a main-follow target (a later ledger of the same or a newer upstream release) when one exists.
func pinTargets(committed string, versions []string) []string {
	out := []string{}
	have := false
	for _, v := range versions {
		if v == committed {
			have = true
		}
	}
	if have {
		out = append(out, committed)
	}
	for _, v := range versions {
		if versionLess(committed, v) {
			out = append(out, v)
		}
	}
	return out
}

// gitOut runs git in root and returns its trimmed standard output.
func gitOut(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	b, err := cmd.Output()
	return strings.TrimSpace(string(b)), err
}

// readLines returns the file's lines, or nil when it cannot be read.
func readLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
