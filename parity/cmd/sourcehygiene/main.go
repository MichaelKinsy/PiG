package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type rule struct {
	code    string
	pattern *regexp.Regexp
	message string
	strings bool
}

var rules = []rule{
	{"SH001", regexp.MustCompile(`(?i)\bphase\s+(?:[0-9]+|[a-z](?:\b|[0-9.]))|\bWS[0-9]+\b|\b(?:lands?|tracked|deferred|planned|scheduled)\s+(?:in|for|to)\s+P[0-9]+\b`), "production comments describe current behavior, not roadmap phases", false},
	{"SH002", regexp.MustCompile(`(?i)\bfuture\s+(?:row|phase|work|worker)\b|\bonce\b[^.\n]{0,80}\blands\b|\bwill be\s+(?:implemented|wired|added)\b`), "future-work promises belong in tracked specifications", false},
	{"SH003", regexp.MustCompile(`(?i)\b(?:TODO|FIXME|HACK|XXX)\b|\b(?:currently|still|temporary|only) (?:a )?placeholder\b|\bplaceholder (?:until|for future)\b|\bscaffold(?:ed)?(?: only|,? not| until)\b|\bstub\b|\btemporary no-op\b`), "production placeholders must be implemented, removed, or tracked outside source", false},
	{"SH004", regexp.MustCompile(`(?i)\bnot yet (?:wired|implemented|supported)\b|\bTODO:`), "user-visible output must not promise unfinished behavior", true},
	{"SH005", regexp.MustCompile(`(?i)\bpig (?:divergence|additive)(?::| [^(])`), "use a typed D<N> marker or neutral translation language", false},
	{"SH006", regexp.MustCompile(`\bGopi\b`), "use the current PiG name", false},
}

type finding struct {
	path    string
	line    int
	code    string
	message string
	text    string
}

const maxGitDiffLineSize = 8 << 20

func main() {
	full := flag.Bool("full", false, "scan every production Go file")
	staged := flag.Bool("staged", false, "scan staged added lines")
	base := flag.String("diff-base", "HEAD", "scan added lines since this git revision")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	lines := map[string]map[int]struct{}{}
	if !*full {
		lines, err = changedLines(root, *base, *staged)
		if err != nil {
			fatal(err)
		}
	}
	findings, err := scan(root, lines, *full)
	if err != nil {
		fatal(err)
	}
	if len(findings) == 0 {
		fmt.Println("source hygiene: clean")
		return
	}
	for _, finding := range findings {
		fmt.Printf("%s:%d: %s: %s: %s\n", finding.path, finding.line, finding.code, finding.message, finding.text)
	}
	os.Exit(1)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "source hygiene:", err)
	os.Exit(2)
}

func changedLines(root, base string, staged bool) (map[string]map[int]struct{}, error) {
	args := []string{"diff", "--unified=0", "--no-color", "--relative", "--src-prefix=a/", "--dst-prefix=b/"}
	if staged {
		args = append(args, "--cached")
	} else {
		args = append(args, base)
	}
	args = append(args, "--", ".")
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	result := map[string]map[int]struct{}{}
	var path string
	hunk := regexp.MustCompile(`^@@ -[0-9]+(?:,[0-9]+)? \+([0-9]+)(?:,([0-9]+))? @@`)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 64*1024), maxGitDiffLineSize)
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "+++ b/"); ok {
			path = after
			continue
		}
		match := hunk.FindStringSubmatch(line)
		if path == "" || match == nil {
			continue
		}
		start, _ := strconv.Atoi(match[1])
		count := 1
		if match[2] != "" {
			count, _ = strconv.Atoi(match[2])
		}
		if count == 0 {
			continue
		}
		if result[path] == nil {
			result[path] = map[int]struct{}{}
		}
		for lineNumber := start; lineNumber < start+count; lineNumber++ {
			result[path][lineNumber] = struct{}{}
		}
	}
	return result, scanner.Err()
}

func scan(root string, changed map[string]map[int]struct{}, full bool) ([]finding, error) {
	var findings []finding
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == ".upstream" || name == "tmp" || name == "vendor" || name == "node_modules" || name == "parity" && strings.Contains(filepath.ToSlash(path), "parity/artifacts") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_generated.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "parity/cmd/newscenario/main.go" {
			return nil
		}
		lineSet := changed[relative]
		if !full && len(lineSet) == 0 {
			return nil
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, group := range parsed.Comments {
			for _, comment := range group.List {
				position := fileSet.Position(comment.Pos())
				if !full {
					if _, ok := lineSet[position.Line]; !ok {
						continue
					}
				}
				findings = append(findings, applyRules(relative, position.Line, comment.Text, false)...)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			position := fileSet.Position(literal.Pos())
			if !full {
				if _, ok := lineSet[position.Line]; !ok {
					return true
				}
			}
			findings = append(findings, applyRules(relative, position.Line, literal.Value, true)...)
			return true
		})
		return nil
	})
	return findings, err
}

func applyRules(path string, line int, text string, stringLiteral bool) []finding {
	var findings []finding
	for _, candidate := range rules {
		if candidate.strings != stringLiteral {
			continue
		}
		if candidate.pattern.MatchString(text) {
			findings = append(findings, finding{path: path, line: line, code: candidate.code, message: candidate.message, text: strings.TrimSpace(text)})
		}
	}
	return findings
}
