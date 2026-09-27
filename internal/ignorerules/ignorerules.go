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

// Rule is one pattern from a .gitignore, .ignore, or .fdignore file, already
// prefixed with the directory that holds the file relative to the walk root.
type Rule struct {
	pattern string
	negated bool
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
			rules = append(rules, Rule{pattern: prefix + trimmed, negated: negated})
		}
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
		pattern := strings.TrimSuffix(rule.pattern, "/")
		matched := match(pattern, rel)
		if directory && !matched {
			matched = match(pattern, rel+"/")
		}
		if matched {
			ignored = !rule.negated
		}
	}
	return ignored
}

func match(pattern, candidate string) bool {
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "/") {
		for part := range strings.SplitSeq(candidate, "/") {
			if matched, _ := path.Match(pattern, part); matched {
				return true
			}
		}
	}
	re := regexp.QuoteMeta(pattern)
	re = strings.ReplaceAll(re, `\*\*`, `.*`)
	re = strings.ReplaceAll(re, `\*`, `[^/]*`)
	re = strings.ReplaceAll(re, `\?`, `[^/]`)
	matched, _ := regexp.MatchString(`^`+re+`(?:/.*)?$`, candidate)
	return matched
}
