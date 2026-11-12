// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"
)

//go:embed forbidden.yaml
var forbiddenYAML []byte

//go:embed mandatory.yaml
var mandatoryYAML []byte

type fileMatch struct {
	Paths          []string `yaml:"paths"`
	Exclude        []string `yaml:"exclude"`
	PathPattern    string   `yaml:"path-pattern"`
	ExcludePattern string   `yaml:"exclude-pattern"`

	paths, exclude             []*regexp.Regexp
	pathPattern, excludePatern *regexp.Regexp
}

func (m *fileMatch) compile() error {
	for _, g := range m.Paths {
		re, err := globRegexp(g)
		if err != nil {
			return err
		}
		m.paths = append(m.paths, re)
	}
	for _, g := range m.Exclude {
		re, err := globRegexp(g)
		if err != nil {
			return err
		}
		m.exclude = append(m.exclude, re)
	}
	var err error
	if m.PathPattern != "" {
		if m.pathPattern, err = regexp.Compile(m.PathPattern); err != nil {
			return fmt.Errorf("path-pattern: %w", err)
		}
	}
	if m.ExcludePattern != "" {
		if m.excludePatern, err = regexp.Compile(m.ExcludePattern); err != nil {
			return fmt.Errorf("exclude-pattern: %w", err)
		}
	}
	return nil
}

func (m *fileMatch) matches(name string) bool {
	match := func(res []*regexp.Regexp) bool {
		return slices.ContainsFunc(res, func(re *regexp.Regexp) bool { return re.MatchString(name) })
	}
	if match(m.exclude) || (m.excludePatern != nil && m.excludePatern.MatchString(name)) {
		return false
	}
	if len(m.paths) == 0 && m.pathPattern == nil {
		return true
	}
	return match(m.paths) || (m.pathPattern != nil && m.pathPattern.MatchString(name))
}

type valueRule struct {
	Not       string      `yaml:"not"`
	MinLength int         `yaml:"min-length"`
	NotIn     []string    `yaml:"not-in"`
	Random    *randomRule `yaml:"random"`

	not *regexp.Regexp
}

type randomRule struct {
	Digits   int `yaml:"digits"`
	Distinct int `yaml:"distinct"`
}

func (v *valueRule) accepts(value string, versions map[string]string) bool {
	notIn := make([]string, 0, len(v.NotIn))
	for _, s := range v.NotIn {
		if expanded, err := expandVersions(s, versions); err == nil {
			s = unquoteMeta(expanded)
		}
		notIn = append(notIn, s)
	}
	switch {
	case len(value) < v.MinLength:
		return false
	case v.not != nil && v.not.MatchString(value):
		return false
	case slices.ContainsFunc(notIn, func(s string) bool { return strings.EqualFold(s, strings.TrimSpace(value)) }):
		return false
	case v.Random != nil && !randomLooking(value, v.Random.Digits, v.Random.Distinct):
		return false
	}
	return true
}

type allowEntry struct {
	Path   string `yaml:"path"`
	Reason string `yaml:"reason"`
}

type forbiddenRule struct {
	ID          string         `yaml:"id"`
	Code        string         `yaml:"code"`
	Description string         `yaml:"description"`
	Files       fileMatch      `yaml:",inline"`
	Pattern     string         `yaml:"pattern"`
	Also        string         `yaml:"also"`
	Value       *valueRule     `yaml:"value"`
	Match       string         `yaml:"match"`
	With        map[string]any `yaml:"with"`
	Allow       []allowEntry   `yaml:"allow"`

	pattern, also *regexp.Regexp
}

type mandatoryRule struct {
	ID          string      `yaml:"id"`
	Description string      `yaml:"description"`
	Files       fileMatch   `yaml:",inline"`
	Pattern     string      `yaml:"pattern"`
	ListedIn    *listedRule `yaml:"listed-in"`
}

type listedRule struct {
	File string   `yaml:"file"`
	Keys []string `yaml:"keys"`
}

type contentRules struct {
	Forbidden []forbiddenRule
	Mandatory []mandatoryRule
}

const versionsPath = "variables/versions.yaml"

var versionRefRE = regexp.MustCompile(`\{\{\s*versions\.([A-Za-z0-9_.-]+)\s*(?:\|\s*(minor|url)\s*)?\}\}`)

func loadContentRules(forbidden, mandatory []byte) (contentRules, error) {
	var r contentRules
	for name, doc := range map[string]struct {
		raw []byte
		out any
	}{"forbidden.yaml": {forbidden, &r.Forbidden}, "mandatory.yaml": {mandatory, &r.Mandatory}} {
		dec := yaml.NewDecoder(bytes.NewReader(doc.raw))
		dec.KnownFields(true)
		if err := dec.Decode(doc.out); err != nil {
			return contentRules{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	seen := map[string]bool{}
	for i := range r.Forbidden {
		f := &r.Forbidden[i]
		if err := f.validate(seen); err != nil {
			return contentRules{}, fmt.Errorf("forbidden.yaml: rule %q: %w", f.ID, err)
		}
	}
	for i := range r.Mandatory {
		m := &r.Mandatory[i]
		if err := m.validate(seen); err != nil {
			return contentRules{}, fmt.Errorf("mandatory.yaml: rule %q: %w", m.ID, err)
		}
	}
	return r, nil
}

func uniqueID(id string, seen map[string]bool) error {
	switch {
	case id == "":
		return errors.New("needs an id")
	case seen[id]:
		return errors.New("is declared twice")
	}
	seen[id] = true
	return nil
}

func (f *forbiddenRule) validate(seen map[string]bool) error {
	if err := uniqueID(f.ID, seen); err != nil {
		return err
	}
	switch {
	case f.Code == "" || f.Description == "":
		return errors.New("needs a code and a description")
	case (f.Pattern == "") == (f.Match == ""):
		return errors.New("needs either a pattern or a match")
	case f.Also != "" && f.Pattern == "":
		return errors.New("also narrows a pattern")
	case slices.ContainsFunc(f.Allow, func(a allowEntry) bool { return a.Path == "" || a.Reason == "" }):
		return errors.New("an allow entry needs a path and a reason")
	}
	if err := f.Files.compile(); err != nil {
		return err
	}
	if f.Match != "" {
		return f.validateMatch()
	}
	return f.compilePattern()
}

func (f *forbiddenRule) validateMatch() error {
	m, ok := matchers[f.Match]
	if !ok {
		return fmt.Errorf("unknown match %q", f.Match)
	}
	if err := m.validate(params(f.With)); err != nil {
		return fmt.Errorf("match %s: %w", f.Match, err)
	}
	return nil
}

func (f *forbiddenRule) compilePattern() error {
	var err error
	if f.pattern, err = regexp.Compile(f.Pattern); err != nil {
		return fmt.Errorf("pattern: %w", err)
	}
	if f.Also != "" {
		if f.also, err = regexp.Compile(f.Also); err != nil {
			return fmt.Errorf("also: %w", err)
		}
	}
	if f.Value == nil {
		return nil
	}
	if f.pattern.NumSubexp() < 1 {
		return errors.New("a value rule needs a pattern with a capture group")
	}
	if f.Value.Not != "" {
		if f.Value.not, err = regexp.Compile(f.Value.Not); err != nil {
			return fmt.Errorf("value not: %w", err)
		}
	}
	return nil
}

func (m *mandatoryRule) validate(seen map[string]bool) error {
	if err := uniqueID(m.ID, seen); err != nil {
		return err
	}
	if m.Description == "" {
		return errors.New("needs a description")
	}
	if err := m.Files.compile(); err != nil {
		return err
	}
	if (m.Pattern == "") == (m.ListedIn == nil) {
		return errors.New("needs either a pattern or listed-in")
	}
	if m.Pattern != "" {
		if _, err := regexp.Compile(versionRefRE.ReplaceAllString(m.Pattern, "x")); err != nil {
			return fmt.Errorf("pattern: %w", err)
		}
	}
	if m.ListedIn != nil && (m.ListedIn.File == "" || len(m.ListedIn.Keys) == 0) {
		return errors.New("listed-in needs a file and keys")
	}
	return nil
}

type textFile struct {
	name   string
	lines  []numberedLine
	binary bool
}

func (t textFile) joined() ([]byte, func(int) int) {
	var b bytes.Buffer
	starts := make([]int, 0, len(t.lines))
	for _, l := range t.lines {
		starts = append(starts, b.Len())
		b.WriteString(l.text)
		b.WriteByte('\n')
	}
	return b.Bytes(), func(off int) int {
		i, found := slices.BinarySearch(starts, off)
		if !found {
			i--
		}
		return t.lines[max(i, 0)].n
	}
}

func changedText(d *data, head string) ([]textFile, error) {
	var out []textFile
	for _, f := range d.Files {
		if f.Status == "removed" {
			continue
		}
		if !f.whole() || head == "" {
			out = append(out, textFile{name: f.Filename, lines: addedNumbered(f.Patch)})
			continue
		}
		raw, err := os.ReadFile(filepath.Join(head, filepath.FromSlash(f.Filename)))
		if err != nil {
			return nil, fmt.Errorf("%s has no patch and could not be read whole: %w", f.Filename, err)
		}
		t := textFile{name: f.Filename, binary: bytes.IndexByte(raw, 0) >= 0}
		for i, line := range strings.Split(string(raw), "\n") {
			t.lines = append(t.lines, numberedLine{i + 1, line})
		}
		out = append(out, t)
	}
	return out, nil
}

func firstGroup(sub []string) string {
	for _, g := range sub[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

func unquoteMeta(s string) string {
	return regexp.MustCompile(`\\(.)`).ReplaceAllString(s, "$1")
}

func checkForbidden(rules []forbiddenRule, files []textFile, versions map[string]string) []finding {
	var found []finding
	for _, file := range files {
		text, lineAt := file.joined()
		for _, r := range rules {
			if !r.appliesTo(file) {
				continue
			}
			hits := r.patternHits(file, versions)
			if r.Match != "" {
				hits = matchers[r.Match].run(file.name, text, lineAt, params(r.With))
			}
			for _, h := range hits {
				found = append(found, r.finding(h))
			}
		}
	}
	return dedupeFindings(found)
}

func (r forbiddenRule) appliesTo(file textFile) bool {
	return r.Files.matches(file.name) &&
		!slices.ContainsFunc(r.Allow, func(a allowEntry) bool { return a.Path == file.name }) &&
		(!file.binary || readsBytes(r))
}

func (r forbiddenRule) patternHits(file textFile, versions map[string]string) []finding {
	if r.pattern == nil {
		return nil
	}
	var hits []finding
	for _, l := range file.lines {
		if r.also != nil && !r.also.MatchString(l.text) {
			continue
		}
		for _, sub := range r.pattern.FindAllStringSubmatch(l.text, -1) {
			if r.Value == nil || r.Value.accepts(firstGroup(sub), versions) {
				hits = append(hits, finding{file: file.name, line: l.n})
			}
		}
	}
	return hits
}

func (r forbiddenRule) finding(h finding) finding {
	msg := r.Description
	if h.message != "" {
		msg += " (" + h.message + ")"
	}
	return finding{file: h.file, line: h.line, message: r.Code + ": " + msg}
}

func readsBytes(r forbiddenRule) bool {
	return r.Match != "" && matchers[r.Match].binary
}

func dedupeFindings(found []finding) []finding {
	seen := map[finding]bool{}
	var out []finding
	for _, f := range found {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func loadVersions(head string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(head, filepath.FromSlash(versionsPath)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s is missing at the PR's head: rebase on main", versionsPath)
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", versionsPath, err)
	}
	out := map[string]string{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				walk(strings.TrimPrefix(prefix+"."+k, "."), child)
			}
		default:
			out[prefix] = fmt.Sprint(x)
		}
	}
	walk("", doc)
	return out, nil
}

func expandVersions(pattern string, versions map[string]string) (string, error) {
	var missing []string
	out := versionRefRE.ReplaceAllStringFunc(pattern, func(ref string) string {
		m := versionRefRE.FindStringSubmatch(ref)
		v, ok := versions[m[1]]
		if !ok {
			missing = append(missing, m[1])
		}
		switch m[2] {
		case "minor":
			parts := strings.SplitN(v, ".", 3)
			v = strings.Join(parts[:min(len(parts), 2)], ".")
		case "url":
			v = strings.ReplaceAll(v, "+", "%2B")
		}
		return regexp.QuoteMeta(v)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%s has no %s", versionsPath, strings.Join(missing, ", "))
	}
	return out, nil
}

func mandatoryTargets(rules []mandatoryRule, changed, tracked []string) map[string][]string {
	versionsChanged := slices.Contains(changed, versionsPath)
	out := map[string][]string{}
	for _, r := range rules {
		pool := changed
		if versionsChanged && versionRefRE.MatchString(r.Pattern) {
			pool = tracked
		}
		for _, f := range pool {
			if r.Files.matches(f) {
				out[r.ID] = append(out[r.ID], f)
			}
		}
	}
	return out
}

func checkMandatory(rules []mandatoryRule, targets map[string][]string, head string) []finding {
	var found []finding
	var versions map[string]string
	var versionsErr error
	loaded := false
	for _, r := range rules {
		files := targets[r.ID]
		if len(files) == 0 {
			continue
		}
		if r.ListedIn != nil {
			found = append(found, checkListed(r, files, head)...)
			continue
		}
		pattern := r.Pattern
		if versionRefRE.MatchString(pattern) {
			if !loaded {
				versions, versionsErr = loadVersions(head)
				loaded = true
			}
			var err error
			if versionsErr == nil {
				pattern, err = expandVersions(pattern, versions)
			}
			if err = errors.Join(versionsErr, err); err != nil {
				found = append(found, finding{file: versionsPath, message: r.ID + ": " + err.Error()})
				continue
			}
		}
		re := regexp.MustCompile(pattern)
		for _, f := range files {
			raw, err := os.ReadFile(filepath.Join(head, filepath.FromSlash(f)))
			switch {
			case err != nil:
				found = append(found, finding{file: f, message: r.ID + ": " + r.Description + ": the file could not be read"})
			case !re.Match(raw):
				found = append(found, finding{file: f, message: r.ID + ": " + r.Description})
			}
		}
	}
	return found
}

func checkListed(r mandatoryRule, files []string, head string) []finding {
	var doc any
	if _, err := toml.DecodeFile(filepath.Join(head, filepath.FromSlash(r.ListedIn.File)), &doc); err != nil {
		return []finding{{file: r.ListedIn.File, message: r.ID + ": " + err.Error()}}
	}
	normalized, _ := json.Marshal(doc)
	var tree any
	_ = json.Unmarshal(normalized, &tree)
	var globs []string
	for _, key := range r.ListedIn.Keys {
		for _, v := range jsonPath(tree, key) {
			if s, ok := v.(string); ok {
				globs = append(globs, s)
			}
		}
	}
	var found []finding
	for _, f := range files {
		listed := slices.ContainsFunc(globs, func(g string) bool {
			ok, _ := path.Match(g, f)
			return ok || g == f
		})
		if !listed {
			found = append(found, finding{file: f, message: r.ID + ": " + r.Description})
		}
	}
	return found
}

func runContent(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("content", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataPath := fs.String("data", "", "the run's data.json")
	head := fs.String("head", "", "the PR's head checkout")
	out := fs.String("out", "", "where the findings go")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dataPath == "" || *out == "" {
		_, _ = fmt.Fprintln(stderr, "preflight content: --data and --out are required")
		return 2
	}
	found, err := contentFindings(*dataPath, *head)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight content:", err)
		return 2
	}
	type jsonFinding struct {
		File    string `json:"file"`
		Line    int    `json:"line,omitempty"`
		Message string `json:"message"`
	}
	list := make([]jsonFinding, 0, len(found))
	for _, f := range found {
		list = append(list, jsonFinding{f.file, f.line, f.message})
	}
	if err := writeJSON(*out, list); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight content:", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "%d finding(s)\n", len(found))
	if len(found) > 0 {
		return 1
	}
	return 0
}

func contentFindings(dataPath, head string) ([]finding, error) {
	rules, err := loadContentRules(forbiddenYAML, mandatoryYAML)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		return nil, err
	}
	var d data
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	// A PR's symlink may point anywhere on the runner: flatten before reading.
	if head != "" {
		if err := flattenSymlinks(head); err != nil {
			return nil, err
		}
	}
	texts, err := changedText(&d, head)
	if err != nil {
		return nil, err
	}
	versions, _ := loadVersions(head)
	found := checkForbidden(rules.Forbidden, texts, versions)
	var changedNames []string
	for _, f := range d.Files {
		if f.Status != "removed" {
			changedNames = append(changedNames, f.Filename)
		}
	}
	tracked, err := trackedFiles(head)
	if err != nil {
		return nil, err
	}
	targets := mandatoryTargets(rules.Mandatory, changedNames, tracked)
	if err := checkOut(head, targets); err != nil {
		return nil, err
	}
	return append(found, checkMandatory(rules.Mandatory, targets, head)...), nil
}

func trackedFiles(head string) ([]string, error) {
	if head == "" {
		return nil, nil
	}
	// -z: names with spaces, and without git's quoting.
	out, err := git(context.Background(), "-C", head, "ls-files", "-z")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w\n%s", err, out)
	}
	return strings.FieldsFunc(out, func(r rune) bool { return r == 0 }), nil
}

func checkOut(head string, targets map[string][]string) error {
	var missing []string
	for _, files := range targets {
		for _, f := range files {
			if _, err := os.Stat(filepath.Join(head, filepath.FromSlash(f))); err != nil {
				p, err := sparsePattern(f)
				if err != nil {
					return err
				}
				missing = append(missing, p)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	if out, err := git(context.Background(), append([]string{"-C", head, "sparse-checkout", "add"}, slices.Compact(missing)...)...); err != nil {
		return fmt.Errorf("git sparse-checkout add: %w\n%s", err, out)
	}
	return flattenSymlinks(head)
}
