// Package ignorerules applies .gitignore, .ignore, and .fdignore files the way
// upstream Pi's package manager does when it discovers package resources.
package ignorerules

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Rule is one pattern from a .gitignore, .ignore, or .fdignore file, prefixed with its directory relative to the walk root. Its immutable matcher belongs to that walk; a reload constructs fresh rules.
type Rule struct {
	pattern string
	negated bool
	matcher *regexp.Regexp
}

// Append adds the rules from dir's .gitignore, .ignore, and .fdignore files,
// as upstream Pi's addIgnoreRules (core/package-manager.ts) does.
func Append(rules []Rule, dir, root string) []Rule {
	relDir, err := filepath.Rel(root, dir)
	if err != nil {
		return rules
	}
	prefix := ""
	if relDir != "." {
		prefix = filepath.ToSlash(relDir) + "/"
	}
	for _, name := range []string{".gitignore", ".ignore", ".fdignore"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			negated := strings.HasPrefix(trimmed, "!")
			if negated {
				trimmed = strings.TrimPrefix(trimmed, "!")
			}
			trimmed = strings.TrimPrefix(trimmed, "/")
			rules = append(rules, newRule(prefix+trimmed, negated))
		}
	}
	return rules
}

// AppendPatterns appends already root-relative ignore patterns without filesystem access. It preserves negation and escaping for environment-backed resource loaders.
func AppendPatterns(rules []Rule, patterns []string) []Rule {
	for _, pattern := range patterns {
		for strings.HasSuffix(pattern, " ") && !strings.HasSuffix(pattern, `\ `) {
			pattern = strings.TrimSuffix(pattern, " ")
		}
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		negated := strings.HasPrefix(pattern, "!")
		if negated {
			pattern = pattern[1:]
		}
		rules = append(rules, newRule(pattern, negated))
	}
	return rules
}

// Ignored reports whether candidate, relative to root, is excluded by rules.
func Ignored(candidate string, directory bool, root string, rules []Rule) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	ignored := false
	for _, rule := range rules {
		matched := rule.match(rel)
		if directory && !matched {
			matched = rule.match(rel + "/")
		}
		if matched {
			ignored = !rule.negated
		}
	}
	return ignored
}

func newRule(pattern string, negated bool) Rule {
	pattern = strings.TrimSuffix(pattern, "/")
	rule := Rule{pattern: pattern, negated: negated}
	if pattern == "" {
		return rule
	}
	re := regexp.QuoteMeta(pattern)
	re = strings.ReplaceAll(re, `\*\*`, `.*`)
	re = strings.ReplaceAll(re, `\*`, `[^/]*`)
	re = strings.ReplaceAll(re, `\?`, `[^/]`)
	rule.matcher, _ = regexp.Compile(`^` + re + `(?:/.*)?$`)
	return rule
}

func (rule Rule) match(candidate string) bool {
	if rule.pattern == "" {
		return false
	}
	if !strings.Contains(rule.pattern, "/") {
		for part := range strings.SplitSeq(candidate, "/") {
			if matched, _ := path.Match(rule.pattern, part); matched {
				return true
			}
		}
	}
	return rule.matcher != nil && rule.matcher.MatchString(candidate)
}
