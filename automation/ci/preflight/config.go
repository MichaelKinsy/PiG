// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
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

// tool is a scanner `preflight install` puts in the bin directory: a release archive pinned by SHA-256, or a
// hash-locked pip requirements file.
type tool struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	SHA256  string `yaml:"sha256"`
	URL     string `yaml:"url"`
	Pip     string `yaml:"pip"`
}

type when struct {
	Always bool     `yaml:"always" json:"always,omitempty"`
	Paths  []string `yaml:"paths" json:"paths,omitempty"`
}

type exitCodes struct {
	Clean    []int `yaml:"clean" json:"clean"`
	Findings []int `yaml:"findings" json:"findings"`
}

// reportSpec says where a scan's findings are and how to read file, line and message from each.
type reportSpec struct {
	In      string `yaml:"in" json:"in"`
	Format  string `yaml:"format" json:"format"`
	Pattern string `yaml:"pattern" json:"pattern,omitempty"`
	Ignore  string `yaml:"ignore" json:"ignore,omitempty"`
	List    string `yaml:"list" json:"list,omitempty"`
	File    string `yaml:"file" json:"file,omitempty"`
	Line    string `yaml:"line" json:"line,omitempty"`
	Message string `yaml:"message" json:"message,omitempty"`
	// UnreadFindings is "pass" when a findings exit without a finding read is not an error.
	UnreadFindings string `yaml:"unread-findings" json:"unread_findings,omitempty"`

	pattern, ignore *regexp.Regexp
}

// scanSpec is how a scan check runs its tool.
type scanSpec struct {
	Input          string            `yaml:"input" json:"input"`
	Select         string            `yaml:"select" json:"select,omitempty"`
	Checkout       []string          `yaml:"checkout" json:"checkout,omitempty"`
	Overlay        []string          `yaml:"overlay" json:"overlay,omitempty"`
	Run            []string          `yaml:"run" json:"run"`
	Dir            string            `yaml:"dir" json:"dir,omitempty"`
	Env            map[string]string `yaml:"env" json:"env,omitempty"`
	Exit           exitCodes         `yaml:"exit" json:"exit"`
	Report         reportSpec        `yaml:"report" json:"report"`
	AddedLinesOnly bool              `yaml:"added-lines-only" json:"added_lines_only,omitempty"`

	selectRE *regexp.Regexp
}

// check is one pre-flight check as config.yaml declares it.
type check struct {
	Name  string         `yaml:"name" json:"name"`
	When  when           `yaml:"when" json:"when"`
	Stage string         `yaml:"stage" json:"stage"`
	Reads []string       `yaml:"reads" json:"reads,omitempty"`
	Rule  string         `yaml:"rule" json:"rule,omitempty"`
	With  map[string]any `yaml:"with" json:"with,omitempty"`
	Scan  *scanSpec      `yaml:"scan" json:"scan,omitempty"`
	// Main lists the files in main's tree the check's verdict depends on (globs; a directory counts with everything
	// in it); a scan's overlay counts too. Their content is part of the definition.
	Main []string `yaml:"main" json:"main,omitempty"`
	// Revision is raised by hand to decide the check again where nothing else here changed.
	Revision int `yaml:"revision" json:"revision,omitempty"`
	// Alert, when the check fails, is posted once as a comment of its own that mentions the PR's author: for a finding
	// a summary table must not leave to be noticed, such as a secret.
	Alert string `yaml:"alert" json:"alert,omitempty"`

	patterns []*regexp.Regexp
	// declared is the SHA-256 of the check's declaration here.
	declared string
	// definition is the SHA-256 of the check's declaration and, once bindMain ran, of its main files: part of every
	// input hash, so editing a check or a file it reads decides that check again and no other.
	definition string
}

type config struct {
	// EngineRevision is raised by hand when the engine's code changes what a stored verdict means; it decides every
	// check again. A new scanner or image version does not.
	EngineRevision int     `yaml:"engine-revision"`
	Tools          []tool  `yaml:"tools"`
	Checks         []check `yaml:"checks"`
}

// Stages of a check.
const (
	stageCheap = "cheap"
	stageScan  = "scan"
)

// Inputs of a scan.
const (
	inputAdded   = "added"
	inputChanged = "changed"
	inputTree    = "tree"
)

var sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// sourceNames are the data sources a cheap check may read.
var sourceNames = []string{"pull", "files", "commits", "compare-main", "depends-on", "dependency-graph"}

// loadConfig parses and validates config.yaml.
func loadConfig(data []byte) (config, error) {
	var cfg config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("config: %w", err)
	}
	if cfg.EngineRevision < 1 {
		return config{}, errors.New("config: engine-revision must be 1 or more")
	}
	if len(cfg.Checks) == 0 {
		return config{}, errors.New("config: no checks")
	}
	for i, t := range cfg.Tools {
		if err := t.validate(); err != nil {
			return config{}, fmt.Errorf("config: tool %d: %w", i+1, err)
		}
	}
	seen := map[string]bool{}
	for i := range cfg.Checks {
		c := &cfg.Checks[i]
		if c.Name == "" {
			return config{}, fmt.Errorf("config: check %d has no name", i+1)
		}
		if seen[c.Name] {
			return config{}, fmt.Errorf("config: check %q is declared twice", c.Name)
		}
		seen[c.Name] = true
		if err := c.validate(); err != nil {
			return config{}, fmt.Errorf("config: check %q: %w", c.Name, err)
		}
	}
	return cfg, nil
}

func (t tool) validate() error {
	switch {
	case t.Name == "" || t.Version == "":
		return errors.New("needs a name and a version")
	case t.Pip != "" && (t.URL != "" || t.SHA256 != ""):
		return fmt.Errorf("%s: either pip or url and sha256, not both", t.Name)
	case t.Pip == "" && (!strings.HasPrefix(t.URL, "https://") || !sha256RE.MatchString(t.SHA256)):
		return fmt.Errorf("%s: needs an https url and its lowercase sha256", t.Name)
	}
	return nil
}

func (c *check) validate() error {
	if c.When.Always == (len(c.When.Paths) > 0) {
		return errors.New("when needs either always: true or paths, not both")
	}
	for _, p := range c.When.Paths {
		re, err := globRegexp(p)
		if err != nil {
			return err
		}
		c.patterns = append(c.patterns, re)
	}
	switch c.Stage {
	case stageCheap:
		if c.Scan != nil {
			return errors.New("a cheap check has a rule, not a scan")
		}
		r, ok := builtinRules[c.Rule]
		if !ok {
			return fmt.Errorf("unknown rule %q", c.Rule)
		}
		if len(c.Reads) == 0 {
			return errors.New("a cheap check reads at least one source")
		}
		for _, s := range c.Reads {
			if !slices.Contains(sourceNames, s) {
				return fmt.Errorf("reads unknown source %q", s)
			}
		}
		if err := r.validate(params(c.With)); err != nil {
			return fmt.Errorf("rule %s: %w", c.Rule, err)
		}
	case stageScan:
		if c.Rule != "" || len(c.Reads) > 0 || c.With != nil {
			return errors.New("a scan check has a scan, not a rule")
		}
		if c.Scan == nil {
			return errors.New("a scan check needs scan:")
		}
		if err := c.Scan.validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("stage %q is neither cheap nor scan", c.Stage)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	c.declared = hex.EncodeToString(sum[:])
	c.definition = c.declared
	return nil
}

func (s *scanSpec) validate() error {
	if !slices.Contains([]string{inputAdded, inputChanged, inputTree}, s.Input) {
		return fmt.Errorf("scan input %q is not added, changed or tree", s.Input)
	}
	if len(s.Run) == 0 {
		return errors.New("scan run is empty")
	}
	if i := slices.Index(s.Run, "{files}"); i >= 0 && (i == 0 || s.Run[i-1] != "--") {
		return errors.New("scan run needs -- before {files}, so a file name is never read as an option")
	}
	if len(s.Exit.Clean) == 0 {
		return errors.New("scan exit needs the clean codes")
	}
	for _, code := range s.Exit.Findings {
		if slices.Contains(s.Exit.Clean, code) {
			return fmt.Errorf("exit code %d is both clean and findings", code)
		}
	}
	for _, p := range append(slices.Clone(s.Checkout), s.Overlay...) {
		if p == "" || (strings.HasPrefix(p, "/") && p != "/*") || strings.Contains(p, "..") {
			return fmt.Errorf("path %q must be relative, or /* for the whole tree", p)
		}
	}
	if s.Select != "" {
		re, err := regexp.Compile(s.Select)
		if err != nil {
			return fmt.Errorf("select: %w", err)
		}
		s.selectRE = re
	}
	return s.Report.validate()
}

func (r *reportSpec) validate() error {
	if r.In != "out" && r.In != "stdout" {
		return fmt.Errorf("report in %q is neither out nor stdout", r.In)
	}
	switch r.Format {
	case "json", "json-lines":
		if r.Message == "" {
			return errors.New("a JSON report names the message field")
		}
	case "lines":
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return fmt.Errorf("report pattern: %w", err)
		}
		if !slices.Contains(re.SubexpNames(), "message") {
			return errors.New("report pattern needs a (?P<message>...) group")
		}
		r.pattern = re
	default:
		return fmt.Errorf("report format %q is not json, json-lines or lines", r.Format)
	}
	if r.UnreadFindings != "" && r.UnreadFindings != "pass" {
		return fmt.Errorf("report unread-findings %q is not pass", r.UnreadFindings)
	}
	if r.Ignore != "" {
		re, err := regexp.Compile(r.Ignore)
		if err != nil {
			return fmt.Errorf("report ignore: %w", err)
		}
		r.ignore = re
	}
	return nil
}

// relevant reports whether a check applies to a PR that changes these paths.
func (c check) relevant(changed []string) bool {
	if c.When.Always {
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
	// QuoteMeta escapes * and ?; the replacer then turns the escaped wildcards into patterns, trying them in argument
	// order at each place, so **/ wins over ** and ** over *.
	return regexp.Compile("^" + globReplacer.Replace(regexp.QuoteMeta(glob)) + "$")
}

var globReplacer = strings.NewReplacer(`\*\*/`, "(?:.*/)?", `\*\*`, ".*", `\*`, "[^/]*", `\?`, "[^/]")

// bindMain adds the content of each check's main files in main's tree at root to its definition and returns the run's
// version: the engine revision with every definition. A state of the same version and the same head and base is
// reused whole; otherwise each check keeps its stored verdict while its own definition and inputs are unchanged.
func bindMain(cfg *config, root string) (string, error) {
	run := sha256.New()
	_, _ = fmt.Fprintf(run, "engine %d\x00", cfg.EngineRevision) // a hash.Hash never fails to write
	for i := range cfg.Checks {
		c := &cfg.Checks[i]
		paths := slices.Clone(c.Main)
		if c.Scan != nil {
			paths = append(paths, c.Scan.Overlay...)
		}
		names, err := mainNames(root, paths)
		if err != nil {
			return "", fmt.Errorf("check %s: %w", c.Name, err)
		}
		h := sha256.New()
		_, _ = h.Write([]byte(c.declared))
		for _, n := range names {
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(n)))
			if err != nil {
				return "", fmt.Errorf("check %s: %w", c.Name, err)
			}
			_, _ = fmt.Fprintf(h, "\x00%s\x00%d\x00", n, len(raw))
			_, _ = h.Write(raw)
		}
		c.definition = hex.EncodeToString(h.Sum(nil))
		_, _ = fmt.Fprintf(run, "%s\x00%s\x00", c.Name, c.definition)
	}
	return hex.EncodeToString(run.Sum(nil)), nil
}

// mainNames expands globs and directories under root to the sorted slash paths of the files in them; a path that
// matches nothing counts as absent, so adding it later changes the hash.
func mainNames(root string, paths []string) ([]string, error) {
	var names []string
	for _, p := range paths {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			err := filepath.WalkDir(m, func(f string, e fs.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(root, f)
				names = append(names, filepath.ToSlash(rel))
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}
