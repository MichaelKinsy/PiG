package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// otherTest is one non-Go test file with the number of test cases it declares.
type otherTest struct {
	Path     string
	Language string
	Cases    int
	Class    Class
	Signals  []string
}

var (
	tsCaseRE     = regexp.MustCompile(`(?m)^\s*(?:it|test)(?:\.(?:each|skip|only))?\s*\(`)
	pyCaseRE     = regexp.MustCompile(`(?m)^\s*(?:async\s+)?def (test_\w+)`)
	rsCaseRE     = regexp.MustCompile(`(?m)#\[(?:tokio::)?test\b`)
	snapshotRE   = regexp.MustCompile(`(?i)toMatch(?:Inline)?Snapshot|\binsta\b|assert_snapshot|expect_file|golden|snapshot\.(?:txt|json)`)
	otherOracle  = regexp.MustCompile(`(?i)oracle|\.upstream|earendil-works|pi-coding-agent|node_modules/\.bin/pi`)
	otherSkipDir = regexp.MustCompile(`(?:^|/)(?:node_modules|target|shims|vendor|\.venv)/`)
)

func isOtherTestFile(p string) (string, bool) {
	switch {
	case otherSkipDir.MatchString(p):
		return "", false
	case regexp.MustCompile(`\.(?:test|spec)\.(?:ts|mts|js|mjs|cjs)$`).MatchString(p):
		return "ts", true
	case strings.HasSuffix(p, ".py") && (strings.HasPrefix(path.Base(p), "test_") || strings.HasSuffix(p, "_test.py")):
		return "py", true
	case strings.HasSuffix(p, ".rs"):
		return "rs", true
	}
	return "", false
}

func inventoryOther(root string) ([]otherTest, error) {
	tracked, err := gitFiles(root, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	var out []otherTest
	for _, f := range tracked {
		lang, ok := isOtherTestFile(f)
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return nil, err
		}
		src := string(data)
		var cases int
		switch lang {
		case "ts":
			cases = len(tsCaseRE.FindAllString(src, -1))
		case "py":
			cases = len(pyCaseRE.FindAllString(src, -1))
		case "rs":
			cases = len(rsCaseRE.FindAllString(src, -1))
		}
		if cases == 0 {
			continue
		}
		t := otherTest{Path: f, Language: lang, Cases: cases, Class: ClassUnclassified}
		switch {
		case snapshotRE.MatchString(src):
			t.Class = ClassSelfRef
			t.Signals = append(t.Signals, "snapshot")
		case otherOracle.MatchString(src):
			t.Class = ClassOracle
			t.Signals = append(t.Signals, "oracle")
		case citeRE.MatchString(src):
			t.Class = ClassPiCited
			t.Signals = append(t.Signals, "pi-cite")
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func summarizeOther(tests []otherTest) string {
	type key struct {
		lang  string
		class Class
	}
	files, cases := map[key]int{}, map[key]int{}
	for _, t := range tests {
		k := key{t.Language, t.Class}
		files[k]++
		cases[k] += t.Cases
	}
	var keys []key
	for k := range files {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].lang != keys[j].lang {
			return keys[i].lang < keys[j].lang
		}
		return keys[i].class < keys[j].class
	})
	var b strings.Builder
	b.WriteString("\nnon-Go tests\nlanguage\tclass\tfiles\tcases\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s\t%s\t%d\t%d\n", k.lang, k.class, files[k], cases[k])
	}
	return b.String()
}
