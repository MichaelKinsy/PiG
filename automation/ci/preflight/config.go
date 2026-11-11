// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

//go:embed config.yaml
var configYAML []byte

// check is one pre-flight check as config.yaml declares it.
type check struct {
	Name    string   `yaml:"name"`
	Always  bool     `yaml:"always"`
	Paths   []string `yaml:"paths"`
	Sources []string `yaml:"sources"`
	Version int      `yaml:"version"`
	// Checkout lists what the check's scan reads at the PR's head besides the changed files; "/*" is the whole tree.
	Checkout []string `yaml:"checkout"`

	patterns []*regexp.Regexp
}

type config struct {
	Checks []check `yaml:"checks"`
}

// loadConfig parses and validates config.yaml: every check has a unique name, a positive version, known sources, and
// either always or at least one path pattern, never both.
func loadConfig(data []byte) (config, error) {
	var cfg config
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("config: %w", err)
	}
	if len(cfg.Checks) == 0 {
		return config{}, fmt.Errorf("config: no checks")
	}
	seen := map[string]bool{}
	for i := range cfg.Checks {
		c := &cfg.Checks[i]
		switch {
		case c.Name == "":
			return config{}, fmt.Errorf("config: check %d has no name", i+1)
		case seen[c.Name]:
			return config{}, fmt.Errorf("config: check %q is declared twice", c.Name)
		case c.Version < 1:
			return config{}, fmt.Errorf("config: check %q needs a version of 1 or more", c.Name)
		case c.Always == (len(c.Paths) > 0):
			return config{}, fmt.Errorf("config: check %q needs either always: true or paths, not both", c.Name)
		case len(c.Sources) == 0:
			return config{}, fmt.Errorf("config: check %q reads no source", c.Name)
		}
		seen[c.Name] = true
		for _, s := range c.Sources {
			if !slices.Contains(sourceNames, s) {
				return config{}, fmt.Errorf("config: check %q reads unknown source %q", c.Name, s)
			}
		}
		for _, p := range c.Checkout {
			if p == "" || (strings.HasPrefix(p, "/") && p != "/*") || strings.Contains(p, "..") {
				return config{}, fmt.Errorf("config: check %q: checkout path %q must be relative, or /*", c.Name, p)
			}
		}
		for _, p := range c.Paths {
			re, err := globRegexp(p)
			if err != nil {
				return config{}, fmt.Errorf("config: check %q: %w", c.Name, err)
			}
			c.patterns = append(c.patterns, re)
		}
	}
	return cfg, nil
}

// relevant reports whether a check applies to a PR that changes these paths.
func (c check) relevant(changed []string) bool {
	if c.Always {
		return true
	}
	for _, f := range changed {
		for _, re := range c.patterns {
			if re.MatchString(f) {
				return true
			}
		}
	}
	return false
}

// globRegexp turns a slash-separated glob into an anchored regexp: "**/" matches any number of leading directories,
// "**" any run of characters, "*" any run without a slash, "?" one character that is not a slash.
func globRegexp(glob string) (*regexp.Regexp, error) {
	if glob == "" || strings.HasPrefix(glob, "/") {
		return nil, fmt.Errorf("path pattern %q must be relative and non-empty", glob)
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch ch := glob[i]; {
		case strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case ch == '*':
			b.WriteString("[^/]*")
		case ch == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// versionFiles are the files outside automation/ci/preflight whose change on main makes every stored verdict stale: the
// scanners' configurations and the PR template the template check reads.
var versionFiles = []string{".gitleaks.toml", ".github/pull_request_template.md", ".github/actionlint.yaml", "REUSE.toml"}

// preflightVersion is the SHA-256 over the pre-flight's own files in main's tree at root: everything under
// automation/ci/preflight except acknowledged.toml, which is data, and versionFiles. A stored state with another
// version is not reused.
func preflightVersion(root string) (string, error) {
	var names []string
	dir := filepath.Join(root, "automation", "ci", "preflight")
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || e.Name() == filepath.Base(acknowledgedPath) {
			return err
		}
		names = append(names, p)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("pre-flight version: %w", err)
	}
	for _, f := range versionFiles {
		names = append(names, filepath.Join(root, filepath.FromSlash(f)))
	}
	slices.Sort(names)
	h := sha256.New()
	for _, n := range names {
		raw, err := os.ReadFile(n)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("pre-flight version: %w", err)
		}
		rel, _ := filepath.Rel(root, n)
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(raw)) // a hash.Hash never fails to write
		_, _ = h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
